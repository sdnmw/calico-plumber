---
title: "性能与网络影响评估"
badge: "BENCHMARK"
description: "客观评估 calico-plumber 在数据面延迟、节点 CPU/内存占用、管理面带宽以及按需排障场景下对生产集群的实际影响与调优建议。"
---

## 1. 核心结论总览

| 评估维度 | `eBPF` 模式 | `Legacy` 模式 | 对业务影响等级 |
| :--- | :--- | :--- | :--- |
| **业务转发延迟** | 仅内核哈希表原子累加（几十纳秒/次），不拷贝报文、不修改 skb | 无数据面 Hook，零转发路径介入 | **极低（可忽略）** |
| **业务网络吞吐** | 损耗 < 1%（非逐包 ringbuf 推送） | 0% 直接数据面损耗 | **极低** |
| **Agent 常驻 CPU** | 默认 2s 轮询约 `60m ~ 95m`（优化至 5s 可降至 `<20m`） | 随活跃 Pod Netns 与连接数增长（约 `50m ~ 150m`） | **低（受 Limits 限制）** |
| **Agent 常驻内存** | 约 `85 ~ 150 MiB`（受 65,536 Map 上限封顶） | 约 `60 ~ 120 MiB` | **低** |
| **Agent→Server 带宽** | 每节点约 `10 ~ 50 KB/s`（仅上报增量聚合指标） | 每节点约 `15 ~ 80 KB/s` | **极低** |
| **K8s API Server 压力** | Server 单点每 5s 只读同步，非各节点直连 API Server | 同左 | **极低** |

---

## 2. 集群实测资源数据

在 2 节点 Rocky Linux 9.7（Kernel `5.14`、约 117 Pod、75 Service、活跃 conntrack ~13,000 条）测试集群连续运行超过 14 小时后的实测数据（`kubectl -n calico-netdiag top pods`）：

| 组件 | 节点 | CPU 实测 | 内存实测 | Limits 配置 |
| :--- | :--- | :--- | :--- | :--- |
| `calico-netdiag-agent` | `mw-controlplane` | `94m` | `149 MiB` | `500m / 256Mi` |
| `calico-netdiag-agent` | `mw-workergroup1` | `68m` | `128 MiB` | `500m / 256Mi` |
| `calico-netdiag-server` | `mw-workergroup1` | `28m` | `68 MiB` | `500m / 256Mi` |
| `calico-netdiag-probe` | 全节点 | `0m` | `6 ~ 10 MiB` | `100m / 64Mi` |

> **关键分析**：Agent 的 `68m ~ 94m` CPU **几乎全部来自用户态每 2 秒一次的周期性轮询**（全量读取宿主机 `nf_conntrack` 用于 Service DNAT 关联 + 宿主机接口/BIRD 巡检 + 遍历 BPF Map），而非内核 eBPF 钩子本身。

---

## 3. 各模块影响拆解与安全熔断设计

### 3.1 流量拓扑采集（常驻开销）
- **为什么不会把节点打满**：
  - `eBPF` 模式采用内核态 `LRU_HASH` 预聚合，而非 `perf_event`/`ringbuf` 逐包送往用户态；即使单机达到数十万 PPS，用户态每周期最多只读取 65,536 条聚合记录。
  - Server 内存流量环设有硬上限（最多保留 1 小时且不超过 200,000 条桶记录），防止长时间运行发生内存泄漏。
- **`Legacy` 模式注意事项**：
  - 在单节点运行超过 150 个 Pod 且短连接极高的集群中，每 2~3 秒遍历全部 Netns 执行 `INET_DIAG` 会产生一定 CPU 抖动；建议在 `Legacy` 节点上将 `--poll-interval` 调大至 `5s` 或 `10s`。

### 3.2 按需排障操作（瞬时开销与安全保护）
- **多跳 TraceFlow**：仅在点击执行时触发，每次仅发送 1 个探测包，`AF_PACKET` 监听窗口默认 `2.5s` 到期自动关闭，对集群吞吐无影响。
- **实时 TCPdump 抓包**：高吞吐网卡抓包是运维工具最常见的风险点，平台已强制内置四重保护：
  1. **并发限流**：单节点最多同时允许 `4` 个抓包任务；
  2. **包数与时长硬上限**：默认 `500` 包 / `30s`，最大不超过 `5000` 包 / `120s`；
  3. **截断长度保护**：默认 `snaplen=256` 字节（足够解码 L2~L4 与 HTTP/DNS 头部，避免全载荷拷贝）；
  4. **磁盘自动清理**：临时 `.pcap` 文件存放于容器临时目录，15 分钟后自动回收，且前端断开 WebSocket 时立即 `kill` 底层 `tcpdump` 进程。
- **全局健康检查**：默认每 `60s` 执行一次（支持手动触发），全集群 75 个 Service + 节点网格探活单次并发耗时约 `1s`，单个 TCP 探活超时上限 `1s`，并发信号量限制为 `24`，不会对业务应用产生连接风暴。

---

## 4. 面向大规模生产集群的调优建议

如果你计划在 **50+ 节点或单机高并发（>5 万活跃连接）** 的生产环境长期运行，建议在 `deploy/k8s/` 中做如下参数调优：

1. **放宽采集与巡检周期**：
   - 在 `03-agent-daemonset.yaml` 中为 Agent 增加启动参数 `-poll-interval=5s`（或 `10s`），可将 Agent 常驻 CPU 降低约 60%~75%。
   - 在 `02-server.yaml` 中为 Server 调整 `-health-interval=180s`、`-k8s-sync-interval=15s`。
2. **明确资源隔离**：
   - 保持 `calico-netdiag-agent` 的 `limits.cpu: 500m`、`limits.memory: 256Mi`，确保任何异常流量突发都不会挤占业务 Pod 或 `kubelet`/`calico-node` 资源。
3. **只读权限最小化**：
   - Control Plane `ClusterRole` 全程仅授予 `get / list / watch` 只读权限，绝不修改任何 Kubernetes 或 Calico CRD 配置。
