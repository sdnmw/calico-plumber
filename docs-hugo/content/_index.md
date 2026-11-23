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
| **多跳 TraceFlow** | 真实切入源 Pod Netns 注入唯一特征报文，沿途多节点多接口抓包观测并解包 IPIP/VXLAN/DNAT，精准定位丢包跳点 | 解决跨节点不通、NetworkPolicy 拦截、kube-proxy DNAT 异常 |
| **Pod 排障工作台** | 无需业务容器预装工具，直接执行实时 TCPdump（支持 `.pcap` 下载）、TCP 三次握手时序检测与 HTTP/HTTPS 瀑布流探测 | 业务接口慢、连接重置（RST）、无 Shell 精简镜像抓包 |
| **全局健康中心** | 自动巡检 Pod-Pod 全节点矩阵、Pod-Node、Pod-SVC/CoreDNS 及全集群 Service ClusterIP vs 后端 Pod 穿透可达性 | 日常巡检、变更后网络回归验证、区分 kube-proxy 与应用故障 |

## 系统组件一览

```text
calico-netdiag-server (Deployment × 1, 非特权, 只读 RBAC)
  ├── 内嵌 Web 控制台 (/) + Hugo 技术文档 (/docs/)
  ├── K8s & Calico CRD 实时缓存与多维检索索引
  ├── 时间窗口流量聚合环 (1m / 5m / 15m / 1h)
  ├── 多节点 TraceFlow 编排器 & 全局健康巡检调度器
  │
  ├──► calico-netdiag-agent (DaemonSet, 每节点 1 个, hostNetwork + hostPID)
  │      ├── 自适应流量引擎：eBPF (BTF CO-RE) ⇄ Legacy (Conntrack + INET_DIAG)
  │      ├── Pod Netns 无侵入切入 + AF_PACKET / TCPdump / TCP / HTTP 诊断
  │      └── 宿主机 Route / BIRD / iptables / IPVS / Tunnel 探测
  │
  └──► calico-netdiag-probe (DaemonSet, 每节点 1 个, 普通 Pod 网络, ~6Mi 内存)
         └── 全局 Pod-Pod / Pod-Node / Pod-SVC & CoreDNS 主动探活靶机
```
