---
title: "核心实现原理"
badge: "ARCHITECTURE"
description: "拆解 calico-plumber 六大核心模块在 Linux 内核、Calico 控制面/数据面与 Kubernetes API 层的底层工作机制。"
---

## 1. Calico 网络模式与 kube-proxy 自动感知

calico-plumber 同时结合 **Kubernetes/Calico 控制面 CRD** 与 **宿主机实时内核状态** 双重校验，确保覆盖 BGP、IPIP、VXLAN 等全场景：

1. **控制面 CRD 解析**：通过 `client-go` Dynamic Client 只读采集 `operator.tigera.io/v1`（`Installation`）及 `crd.projectcalico.org/v1`（`IPPool`、`FelixConfiguration`、`BGPConfiguration`、`BGPPeer`、`NetworkPolicy`、`GlobalNetworkPolicy`）：
   - 当 `vxlanMode` 为 `Always` / `CrossSubnet` 时判定为 **VXLAN**（自动获取 VNI、UDP 端口 4789 及节点 `projectcalico.org/IPv4VXLANTunnelAddr`）；
   - 当 `ipipMode` 为 `Always` / `CrossSubnet` 时判定为 **IPIP**（自动获取 `tunl0` 隧道地址）；
   - 当两者均为 `Never` 且 BGP 开启时判定为 **BGP 直连**；
   - 针对 `CrossSubnet` 模式，进一步比对源/目标节点所属子网掩码，准确区分同网段直连与跨网段封装。
2. **BIRD BGP 实时邻居直连读取**：Agent 以只读方式挂载宿主机 `/var/run/calico`，通过 Go 原生 Unix Domain Socket 直接连接 `/var/run/calico/bird.ctl` 发送 `show protocols` 文本协议并解析 BGP Mesh / ToR Peer 会话状态（`Established` / `Idle` / `Connect`），不依赖容器内安装 `birdcl`。
3. **kube-proxy 数据面双解析**：读取 `kube-system/kube-proxy` ConfigMap 并结合节点实时网卡探测：
   - 存在 `kube-ipvs0` 接口 → **IPVS 模式**，解析 `ipvsadm -Ln` 提取虚拟服务调度算法（`rr`/`lc`/`sh`）与后端 RealServer；
   - 检测到 `nft list tables` 含 `kube-proxy` → **nftables 模式**；
   - 默认或常规场景 → **iptables 模式**，解析 `iptables-save -t nat` 还原 `KUBE-SERVICES → KUBE-SVC-* → KUBE-SEP-*` 完整 DNAT 链与 `cali-nat-outgoing` SNAT 规则。

---

## 2. IPPool / IPAM 位图解析与全维度检索

Calico IPAM 以固定大小 CIDR Block（默认 `/26` = 64 IP）分配给节点亲和（`BlockAffinity`）。

- **逐槽位还原**：读取每个 `IPAMBlock` 的 `spec.cidr`、`spec.affinity`、`spec.allocations[]`（数组下标对应 CIDR 起始 IP 的偏移量）与 `spec.attributes[]`（记录 `handle_id` 与 Pod 元数据），与当前存活的 K8s Pod 列表做交叉关联，将每个 IP 精准归类：
  - **活跃 Pod（`pod`）**：已分配且对应运行中 Pod 位于当前亲和节点；
  - **隧道 IP（`tunnel`）**：`handle_id` 包含 `ipip-tunnel` / `vxlan-tunnel` / `wireguard-tunnel`；
  - **跨节点借用（`borrowed`）**：Pod 实际调度节点与 `IPAMBlock` 亲和节点不一致（常见于节点 Block 耗尽时的跨块借用，易引发额外 `/32` 路由碎片）；
  - **孤儿残留（`orphan`）**：`IPAMBlock` 中仍标记占用，但 K8s API 中已无对应存活 Pod（典型的 IP 泄漏故障信号）；
  - **空闲（`free`）**：`allocations[i] == null`。
- **多维工作负载反查**：遍历 `OwnerReferences` 建立 `Pod → ReplicaSet → Deployment`、`StatefulSet`、`DaemonSet` 映射，支持输入任意 Pod IP、ClusterIP、Node IP、Pod 名、工作负载名或 `key=value` 标签毫秒级检索并展开关联 Pods。

---

## 3. 无侵入 Pod Netns 定位与切换机制

生产环境中大量业务容器使用 `distroless` / `scratch` 精简镜像，内部没有 `sh`、`curl`、`ping`、`tcpdump`。calico-plumber 的 Agent 通过 **宿主机视角切入 Pod 网络命名空间** 彻底解决该痛点：

1. **通用 Netns 定位（零运行时绑定）**：Agent 启用 `hostPID: true`，遍历 `/proc/<pid>/cgroup` 匹配目标 Pod UID（同时兼容 systemd cgroup 的下划线 UUID 格式与 cgroupfs 的短横线格式），找到首个非宿主机 Netns inode 的容器进程，拿到 `/proc/<pid>/ns/net`，不依赖 `containerd.sock` 或 `docker.sock`。
2. **宿主机 `cali*` veth 定位**：在宿主机执行 Netlink `RouteGet(PodIP)`，直接命中 Calico 为本机 Pod 下发的 `/32` 路由及其出接口（如 `cali58c52e7a595`）。
3. **线程安全执行**：
   - Go 探测（TCP 握手、HTTP 探测、TraceFlow 发包）：使用 `runtime.LockOSThread()` 绑定当前 Goroutine 到独立 OS 线程，执行 `setns()` 切入目标 Pod Netns，并在自定义 `net.Resolver` 与 `DialContext` 中禁用 Happy Eyeballs 并发协程，确保 **DNS 解析与 TCP/TLS 建连 100% 发生在目标 Pod 网络命名空间内**，结束后恢复原 Netns 并解锁线程。
   - TCPdump 抓包：通过 `nsenter --net=/proc/<pid>/ns/net -- tcpdump ...` 启动独立进程，彻底规避多线程进程 `fork/exec` 切 Netns 的竞态问题。

---

## 4. 多跳 TraceFlow 实时抓包与丢包定位原理

不同于仅靠读配置做“静态纸面推演”的工具，calico-plumber 采用 **真实发包 + 多节点多接口并发 wire-level 观测** 的混合架构：

```text
[源 Pod eth0] ─► [源 cali* veth] ─► [Egress Policy] ─► [kube-proxy DNAT] ─► [内核路由]
                                                                               │
[目标 Pod eth0] ◄─ [目标 cali* veth] ◄─ [Ingress Policy] ◄─ [目标物理口/隧道口] ◄─ [源物理口/隧道口]
```

1. **生成唯一流量指纹**：编排器为本次追踪分配高段唯一临时源端口（TCP/UDP：`49152~59151`）或唯一 `ICMP Echo ID`，避免与集群业务流量混淆。
2. **并发武装双节点 `AF_PACKET` 监听器**：
   - 源节点 Agent 同时在 `src_pod_eth0`（Pod Netns 内）、`src_cali_veth`、隧道口（`tunl0` / `vxlan.calico`）、主物理网卡（如 `ens4`）上开启原始 `AF_PACKET` 监听；
   - 跨节点场景下，目标节点 Agent 提前 120ms 在目标物理网卡、隧道口、`dst_cali_veth`、`dst_pod_eth0` 上武装监听；
   - 监听解析器原生兼容 **L2 以太网帧** 与 **L3 纯 IP 帧（`tunl0`）**，并在物理网卡层自动剥离 **IPIP（IP Protocol 4）** 与 **VXLAN（UDP/4789 + VNI）** 外层封装头，同时记录外层隧道信息展示在前端。
3. **注入真实探测报文并同步采集控制面上下文**：源 Agent 切入源 Pod Netns 发起真实连接/探针，并同步提取宿主机路由表项、`kube-proxy` 命中的 `KUBE-SVC/SEP` 或 IPVS 规则以及 `cali-fw`/`cali-tw` 策略链。
4. **精准丢包归因**：
   - 若正向报文在 `src_cali_veth` 可见但未离开 `src_phy_nic` → 定位为 **源节点 Egress NetworkPolicy 拦截或缺少路由**；
   - 若已离开 `src_phy_nic` 但未到达 `dst_phy_nic` → 定位为 **底层物理网络 / 防火墙 / 隧道端口（IP Proto 4 或 UDP 4789）阻断**；
   - 若已到达 `dst_phy_nic` 但未进入 `dst_cali_veth` → 定位为 **目标节点 Ingress NetworkPolicy 拦截或解封装/本地路由异常**；
   - 若收到 `TCP RST` → 明确区分网络层全通、仅目标端口未监听。

---

## 5. TCPdump、TCP 三次握手与 HTTP 瀑布流原理

- **实时 TCPdump**：使用 `tcpdump -i <iface> -nn -U -s <snaplen> -c <max> -w - <BPF>` 输出标准 pcap 流，服务端通过 `io.TeeReader` 一路写入临时 `.pcap` 文件供用户下载到 Wireshark，另一路交由 Go `gopacket/pcapgo` 实时解码成结构化帧（时间偏移、五元组、TCP Flags、DNS/ICMP 摘要与 Hex/ASCII 载荷）经 WebSocket 推送至浏览器。
- **TCP 三次握手检测**：先在目标 Pod `eth0` 上挂起短时 `AF_PACKET` 监听器，再在 Pod Netns 内完成 DNS 解析与真实 TCP 建连，提取真实报文层的 `SYN`、`SYN-ACK`、`ACK`（或 `RST`）微秒级时间戳，直接绘制真实交互梯形图。
- **HTTP / HTTPS 瀑布流**：结合 `net/http/httptrace.ClientTrace` 的 7 个生命周期回调（`DNSStart/Done`、`ConnectStart/Done`、`TLSHandshakeStart/Done`、`WroteRequest`、`GotFirstResponseByte`），在 Pod Netns 内精确拆解 DNS / TCP / TLS / TTFB / Transfer 五段耗时并读取对端 TLS 协议版本与证书有效期。

---

## 6. 全局网络健康检查原理

集群每节点部署一个极简普通 Pod 网络的 `calico-netdiag-probe` 容器（仅占 ~6MiB 内存），避免借用业务 Pod 执行定时巡检：

1. **Pod-Pod N×N 矩阵**：Server 并发指令每个节点的 Probe Pod 向全集群所有节点（含同节点与跨节点）的 Probe Pod IP 发起 HTTP `/ping`，测量真实 Calico 转发连通性与延迟；
2. **Pod-Node 与 Pod-SVC/CoreDNS**：各节点 Probe Pod 分别测试所有 Node InternalIP、CoreDNS 域名解析（`kubernetes.default.svc.cluster.local`）及核心 ClusterIP 可达性；
3. **全集群 Service 后端穿透体检**：由节点 Agent 并发对每个 Service 的 `ClusterIP:Port` 以及 `EndpointSlice` 中的每个后端 `PodIP:TargetPort` 执行 TCP 探测。若**后端 Pod 直连成功但 ClusterIP 不可达**，立即告警指向 `kube-proxy` 转发异常；若两者均失败则指向应用未监听或策略拦截。

---

## 7. 自适应双引擎流量可视化（`eBPF` 与 `Legacy`）

为了同时兼顾**现代内核的极致精度**与**老版本内核集群的广泛兼容性**，Agent 启动时自动检测 `/sys/kernel/btf/vmlinux` 与 BPF 加载能力，在两种模式间无缝自适应：

### 模式一：`eBPF`（CO-RE，默认优先）

- **编译期**：使用 `clang -target bpf` + `bpf2go` 基于最小化 `__attribute__((preserve_access_index))` 内核结构声明生成 CO-RE 字节码并直接嵌入 Go 二进制，**目标节点无需安装 kernel-headers 或编译器**。
- **运行期挂载 7 个内核 kprobe**：
  - `tcp_sendmsg` / `tcp_cleanup_rbuf`：统计 TCP 发送/接收字节数与调用次数；
  - `tcp_rcv_established`：直接通过 CO-RE 读取内核 `struct tcp_sock->srtt_us >> 3`，获取内核协议栈真实维护的微秒级平滑 RTT；
  - `tcp_retransmit_skb` / `tcp_reset`：实时计数 TCP 重传与连接重置（RST）；
  - `udp_sendmsg` / `skb_consume_udp`：统计 UDP 流量（同时兼容 `connect` 连接态与 DNS 这类无连接 `sendto` 读取 `msg->msg_name`）。
- **内核态聚合 + NAT 关联**：所有指标在内核 `BPF_MAP_TYPE_LRU_HASH`（上限 65,536 条）原位累加，用户态周期读取差值，并结合 `nf_conntrack` 表还原 Service ClusterIP DNAT 映射。

### 模式二：`Legacy`（Conntrack + `INET_DIAG`，老内核自动降级）

当节点内核不支持 BTF、禁止 BPF 系统调用或手动配置 `TELEMETRY_MODE=legacy` 时自动启用：

- **全量五元组与 NAT 映射**：通过 Linux Netlink `nf_conntrack` 获取当前节点所有活跃 TCP/UDP/ICMP 五元组及 Service Pre-NAT / Post-NAT 映射；
- **零侵入获取真实吞吐与 TCP 延迟**：遍历节点上所有去重后的 Pod & Host Netns，发起标准 Linux Netlink `SOCK_DIAG`（`INET_DIAG_INFO`）查询，直接读取内核 `struct tcp_info` 中的 `tcpi_rtt`（微秒级延迟）、`tcpi_bytes_acked`、`tcpi_bytes_received`、`tcpi_segs_out/in` 与 `tcpi_total_retrans`，**无需修改宿主机的 `nf_conntrack_acct` 参数**即可在 Linux 3.10+ 老内核上稳定输出流量大小与连接延迟。

### 中心聚合与多粒度拓扑生成

Server 侧维护内存时间窗口环形缓冲（保留最近 1 小时数据，零外部数据库依赖）：
- 将客户端临时端口（`>= 32768`）到服务端监听端口规范化为统一流向；
- 跨节点流会在源/目的两节点同时被采集，Server 按标准五元组去重避免流量翻倍，并合并 Service 身份与 RTT 采样；
- 支持按 `Namespace`、`Workload`、`Pod`、`Node` 四种粒度实时聚合成有向图，边宽按对数映射吞吐量，边色按延迟/重传/RST 阈值自动标记健康、告警或严重异常。
