---
title: "常用使用教程与实战手册"
badge: "PLAYBOOK"
description: "从零部署 calico-plumber、切换 eBPF / Legacy 模式，以及 9 类典型 Calico 网络故障的标准排障操作指南。"
---

## 1. 快速部署与镜像说明

### 官方公共镜像（Docker Hub）

全部组件已构建 `linux/amd64` 镜像并发布至 `docker.io/lammw12`：

| 组件 | 镜像地址 | 作用 |
| :--- | :--- | :--- |
| 控制面与 Web UI | `docker.io/lammw12/calico-netdiag-server:v0.1.0` | API 服务 + 内嵌前端 + Hugo 文档站 |
| 节点数据面 Agent | `docker.io/lammw12/calico-netdiag-agent:v0.1.0` | eBPF/Legacy 采集 + Netns 排障引擎 |
| Pod 网络探测靶机 | `docker.io/lammw12/calico-netdiag-probe:v0.1.0` | 全局 Pod-Pod/Node/SVC 健康探活 |

### 一键部署到 Kubernetes

```bash
kubectl apply -f deploy/k8s/
kubectl -n calico-netdiag get pods -o wide
```

默认通过 NodePort **`31880`** 暴露 Web 控制台：`http://<任意集群节点IP>:31880`。

---

## 2. 切换 `eBPF` 与 `Legacy` 流量采集模式

Agent 默认采用 `TELEMETRY_MODE=auto`（有 BTF 自动用 `eBPF`，无 BTF 自动降级为 `Legacy`）。如需针对特定集群手动指定模式，修改 DaemonSet 环境变量即可：

```bash
# 强制切换为 Legacy 模式（Conntrack + INET_DIAG，兼容 Linux 3.10+ 老内核）
kubectl -n calico-netdiag set env ds/calico-netdiag-agent TELEMETRY_MODE=legacy

# 恢复自动模式（优先 eBPF，不满足自动降级 Legacy）
kubectl -n calico-netdiag set env ds/calico-netdiag-agent TELEMETRY_MODE=auto
```

切换后在 **架构总览** 页面的“集群节点与自适应流量采集引擎”表格中，可实时看到每个节点的引擎标签（绿色 `eBPF` 或蓝色 `Legacy`）及具体判定原因。

---

## 3. 九大典型故障排障实战手册

### 场景一：跨节点 Pod 之间访问不通

1. **先查底座**：进入 **架构总览** 或 **网络体检与审计**，确认当前 Calico 模式（BGP / IPIP / VXLAN）、BIRD BGP 邻居是否全部为 `Established`，以及两节点网卡 MTU 是否一致。
2. **一键追踪**：直接进入 **TraceFlow** 点击内置的 **「跨节点 Pod → Pod」** 快速场景，或按快捷键 `⌘K` 搜索源 Pod 点击 **TraceFlow**。
3. **选择目标 Pod**、协议（`TCP`/`ICMP`）与端口，点击 **执行 Trace**（也可随时点击右上角 **「逐跳演示」** 按真实节点分层逐步回放数据包流向）。
4. **看分层拓扑定位断点**：
   - 报文停在源节点 `Calico Egress Policy` → 检查源 Pod 命中的 Egress NetworkPolicy；
   - 报文已从源 `Host NIC` 发出但目标 `Host NIC` 未收到 → 检查物理交换机 BGP 路由、云防火墙是否放行 IPIP（IP 协议 4）或 VXLAN（UDP 4789）；
   - 报文到达目标 `Host NIC` 但未进入目标 `cali* veth` → 检查目标 Pod 的 Ingress NetworkPolicy；
   - 返回黄色 `RESET` → 网络完全打通，目标容器进程未监听对应端口。

### 场景二：访问 Service ClusterIP 失败，如何区分 kube-proxy 还是后端应用故障？

1. 进入 **全局健康** → 下方 **全集群 Service & 后端 Endpoint 健康审计**，筛选 `broken` 或 `degraded`。
2. 展开目标 Service 行，直接对比两列实测结果：
   - **后端 Pod TCP 可达，但 ClusterIP 失败** → 明确为 `kube-proxy`（iptables/IPVS 规则）异常；
   - **后端 Pod TCP 也失败** → 后端应用进程异常、端口映射错误或目标策略阻断；
   - **状态为 `no-endpoints`** → Service Selector 标签与 Pod 标签不匹配，或 Pod ReadinessProbe 未就绪。
3. 点击该行右侧 **TraceFlow**，可在泳道中直接查看 `kube-proxy` 命中的 `KUBE-SVC-*` / `IPVS` 规则及 DNAT 转发后的真实后端 Pod IP。

### 场景三：定位集群内异常 TCP 重传、RST 或调用慢链路

1. 进入 **流量拓扑**，在左上角选择需要排查的 Namespace（系统会自动切换为清晰的 **Workload** 粒度），时间窗口选择 `5m` 或 `15m`。
2. 优先关注**橙色（警告）**与**红色（异常）**连线，点击连线上的标签胶囊打开右侧 **五元组连接明细抽屉**。
3. 查看该链路的实时吞吐、Avg/P95 延迟以及具体的重传/RST 次数和精确五元组（`SrcIP:Port → DstIP:Port`）。
4. 直接点击抽屉内单条五元组右侧的 **Trace** 或 **抓包**，无缝定位根因。

### 场景四：业务容器是 distroless 无 Shell 镜像，如何实时抓包并用 Wireshark 分析？

1. 进入 **IPAM 与搜索**（或按 `⌘K`）搜到目标 Pod，点击 **抓包** 进入 **Pod 排障工作台 → TCPdump 实时抓包**。
2. 选择抓包位置：
   - `Pod 内部 eth0 (Netns)`：查看业务容器视角收发的明文包；
   - `宿主机 Calico veth (cali*)`：对比是否被宿主机侧策略丢弃；
   - `tunl0` / `vxlan.calico` / `宿主机主物理网卡`：核查隧道封装与底层物理口收发。
3. 输入标准 BPF 过滤条件（或直接点选快捷标签如 `TCP SYN/RST`、`DNS (53)`），点击 **开始抓包**。
4. 实时点击任意数据包行可在底部查看 Hex/ASCII 载荷；停止或结束后点击右上角 **下载 `.pcap`** 即可导入 Wireshark 深度分析。

### 场景五：微服务 HTTP / HTTPS 接口偶发响应慢，定位慢在 DNS、TLS 还是后端业务

1. 进入 **Pod 排障工作台**，选中发起调用的客户端 Pod。
2. 切换到 **HTTP / HTTPS 瀑布流探测** 标签页，输入集群内服务 URL（如 `https://kubernetes.default.svc.cluster.local:443/healthz` 或业务 `http://<svc>.<ns>.svc:<port>/api`），按需填写 `Host` 请求头。
3. 点击 **发送探测**，观察五段彩色耗时条：
   - **DNS 解析偏高** → 检查 CoreDNS 负载、`/etc/resolv.conf` `ndots:5` 搜索域放大或 conntrack 丢包；
   - **TCP 建连偏高** → 结合 **TCP 三次握手检测** 确认是否存在 SYN 重传；
   - **TLS 握手偏高** → 检查证书链与服务端 CPU；
   - **TTFB 等待偏高** → 网络建连已迅速完成，瓶颈位于后端应用处理或数据库查询。

### 场景六：排查节点 IPAM 分配不均、跨节点借用或孤儿 IP 泄漏

1. 进入 **IPAM 与搜索**，查看顶部各 `IPPool` 的利用率水位与封装模式。
2. 在 **节点 IPAM Block 分配热力图** 中重点关注：
   - **橙色格（跨节点借用 `borrowed`）**：说明某节点本地 Block 已满并借用了其他节点的 CIDR 块，易引发大量 `/32` 宿主机路由；
   - **红色格（孤儿残留 `orphan`）**：IPAM 仍记录占用但集群已无对应 Pod，常见于节点异常断电或 CNI DEL 未执行完成，可结合 `calicoctl ipam check` 进行清理。
3. 点击任意绿色 Pod 色块，可在右侧抽屉查看其宿主机 `cali*` veth、MAC、MTU、Pod 路由表、关联 Service 及命中的全部 NetworkPolicy。

### 场景七：小包能通、大包或 TLS 握手卡死——排查 Calico 网络模式与 MTU 黑洞

1. 进入 **网络体检与审计 → 网络模式 · MTU · NAT 体检**，查看 **Calico 网络模式与 MTU 封装链路** 卡片：
   - 确认当前封装开销（BGP 直连 `0B`、IPIP `20B`、VXLAN `50B`）以及各节点 `物理 NIC → 隧道接口 → 容器 veth` 三层 MTU 是否对齐；
   - 若存在跨节点物理 MTU 不一致或容器 veth MTU 大于 `物理 MTU - 封装开销`，系统会自动标红提示。
2. 在页面下方 **Path MTU / DF 大包黑洞探测** 卡片中，选择源 Pod 并一键点选对端节点探针或目标 IP，点击 **执行 DF 探测**。
3. 观察各字节阶梯的实测结果（`ECHO_REPLY` / `FRAG_NEEDED` / `TIMEOUT`），若大包超时而小包通畅，即可锁定沿途存在 MTU 黑洞并依据实测值调整 Calico MTU。

### 场景八：偶发 DNS 5 秒超时、路由丢失或二层 ARP 故障排查

1. **查 BGP 邻居与三层路由**：进入 **网络体检与审计 → Calico BGP 邻居与三层路由**，核对 BIRD BGP 邻居是否全部 `Established`、**Expected vs Actual 路由一致性审计** 是否存在缺失 Block 路由、幽灵 Pod `/32` 路由或去回程非对称路径，并展开节点主路由表按 `BIRD / 本地 Pod /32 / Blackhole` 检索。
2. **查二层 MAC 与 ARP**：切换到 **二层 MAC 与 ARP 邻居表**，检查默认网关与同网段集群节点的 ARP 状态（是否存在 `FAILED / INCOMPLETE`）、`cali*` Proxy ARP 启用率以及 ARP 表水位。
3. **查 NAT 端口竞态与内核证据**：回到 **网络模式 · MTU · NAT 体检**，查看 **网络异常体检与根因排查** 卡片；如需进一步确认多核 SNAT / UDP 端口冲突计数，点击单条异常右侧的 **查看内核证据 (Conntrack / Sysctl)** 或展开底部 **高级深度排障** 面板即可核查原始指标并一键复制修复命令。

### 场景九：一键导出故障现场诊断包用于工单流转与离线复盘

1. 在以下任意位置点击导出即可下载 `calico-plumber-diag-<timestamp>.zip`：
   - 页面顶部全局导航栏 **「导出诊断包」**（全集群全景诊断包）；
   - **网络体检与审计** 页面右上角 **「一键导出诊断包 (.zip)」**；
   - **TraceFlow** 诊断结论栏右侧 **「导出本次诊断包」**（自动附带本次 `traceId` 的完整逐跳抓包与判定记录）。
2. 解压后用浏览器直接打开 **`report.html`**（无需联网，支持打印为 PDF），或将 `01..07-*.json` 原始数据连同工单发给网络/SRE 团队协作分析。
