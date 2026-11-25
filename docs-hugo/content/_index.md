---
title: "calico-plumber 技术文档"
description: "面向 Kubernetes + Calico 生产集群的一体化网络诊断、IPAM 全景分析与自适应双引擎流量可视化平台。"
---

## 平台能力地图

| 模块 | 核心价值 | 适用场景 |
| :--- | :--- | :--- |
| **架构总览** | 自动识别 Calico 封装模式（BGP / IPIP / VXLAN / CrossSubnet）、`kube-proxy` 模式（iptables / IPVS）、BIRD BGP 会话与各节点采集引擎（`eBPF` / `Legacy`） | 快速摸底陌生集群网络底座、核查 BGP 邻居与 MTU 一致性 |
| **流量拓扑** | 基于真实五元组构建多粒度（Namespace / Workload / Pod / Node）动态依赖图，呈现流量速率、延迟（Avg/P95/Max）与 TCP 重传/RST | 定位服务调用瓶颈、异常重传链路、突发东西向流量 |
| **IPAM 与搜索** | 可视化呈现各节点 `IPAMBlock` 位图分配（活跃 / 隧道 / 借用 / 孤儿 / 空闲），支持按 IP、Pod、Deployment、Service 与标签毫秒级检索 | 排查 IP 耗尽、IPAM 孤儿泄漏、快速反查任意 IP 归属 |
| **多跳 TraceFlow** | 真实切入源 Pod Netns 注入唯一特征报文，按「Node 底层 → 物理网卡 → OS 内核态/用户态 → 虚拟交换/隧道 → 容器 eth0」真实分层架构还原逐跳抓包与流动动画，精准定位丢包跳点 | 解决跨节点不通、NetworkPolicy 拦截、kube-proxy DNAT 与 SNAT 异常 |
| **Pod 排障工作台** | 无需业务容器预装工具，直接执行实时 TCPdump（支持 `.pcap` 下载）、TCP 三次握手时序检测与 HTTP/HTTPS 瀑布流探测 | 业务接口慢、连接重置（RST）、无 Shell 精简镜像抓包 |
| **网络体检与审计** | 从网络模式与 MTU 封装链路、Calico BGP 邻居与 Expected vs Actual 路由一致性审计、二层 MAC/网关 ARP/Proxy ARP 邻居表、NAT 转发与 Path MTU DF 探测逐层体检，发现异常后按需展开底层 Conntrack 与 Sysctl 证据 | 排查小包通大包卡死（MTU 黑洞）、幽灵路由/非对称路由、ARP 解析失败、偶发 DNS 5 秒超时与 SNAT 端口竞态 |
| **全局健康中心** | 自动巡检 Pod-Pod 全节点矩阵、Pod-Node、Pod-SVC/CoreDNS 及全集群 Service ClusterIP vs 后端 Pod 穿透可达性 | 日常巡检、变更后网络回归验证、区分 kube-proxy 与应用故障 |
| **一键诊断包导出** | 顶部导航栏、审计页与 TraceFlow 结论栏一键打包离线 `report.html` 与全量结构化 JSON 现场证据（`.zip`） | 故障现场留档、跨团队工单流转与离线复盘 |

## 系统组件一览

```text
calico-netdiag-server (Deployment × 1, 非特权, 只读 RBAC)
  ├── 内嵌 Web 控制台 (/) + Hugo 技术文档 (/docs/)
  ├── K8s & Calico CRD 实时缓存与多维检索索引
  ├── 时间窗口流量聚合环 (1m / 5m / 15m / 1h) & BGP 状态翻转时间线
  ├── 多节点 TraceFlow 编排器 & 全局健康巡检调度器
  ├── 网络模式/MTU/L2/NAT 审计引擎 + Expected vs Actual 路由比对器 + 诊断包打包器 (.zip)
  │
  ├──► calico-netdiag-agent (DaemonSet, 每节点 1 个, hostNetwork + hostPID)
  │      ├── 自适应流量引擎：eBPF (BTF CO-RE) ⇄ Legacy (Conntrack + INET_DIAG)
  │      ├── Pod Netns 无侵入切入 + AF_PACKET / TCPdump / TCP / HTTP / Path MTU DF 探测
  │      └── 宿主机 Route / BIRD / L2 MAC & ARP / Proxy ARP / iptables / IPVS / Conntrack 采集
  │
  └──► calico-netdiag-probe (DaemonSet, 每节点 1 个, 普通 Pod 网络, ~6Mi 内存)
         └── 全局 Pod-Pod / Pod-Node / Pod-SVC & CoreDNS 主动探活靶机
```
