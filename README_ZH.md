<p align="center">
  <img src="web/public/logo.png" alt="calico-plumber logo" width="148" />
</p>

<h1 align="center">calico-plumber</h1>

<p align="center">
  <strong>容器化 Kubernetes Calico 网络诊断与流量可视化平台</strong><br />
  面向生产集群的一站式 Calico 排障工具箱：BGP / IPIP / VXLAN 全模式感知 · 自适应 eBPF / Legacy 双引擎流量拓扑 · 多跳 TraceFlow · 零侵入 Pod 抓包与握手探测 · 全局网络体检
</p>

<p align="center">
  <a href="README.md">English</a> · <strong>简体中文</strong>
</p>

<p align="center">
  <a href="https://hub.docker.com/u/lammw12"><img src="https://img.shields.io/badge/Docker%20Hub-lammw12%2Fcalico--netdiag-2496ED?logo=docker&logoColor=white" alt="Docker Hub" /></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26" />
  <img src="https://img.shields.io/badge/React-19%20%2B%20TypeScript-61DAFB?logo=react&logoColor=black" alt="React 19" />
  <img src="https://img.shields.io/badge/Kubernetes-Calico%20CNI-326CE5?logo=kubernetes&logoColor=white" alt="Kubernetes Calico" />
  <img src="https://img.shields.io/badge/Telemetry-eBPF%20%2F%20Legacy-10B981" alt="eBPF / Legacy" />
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License" /></a>
</p>

<p align="center">
  <a href="#一项目功能简介">项目简介</a> ·
  <a href="#二快速部署方式">部署方式</a> ·
  <a href="#三七大核心功能与演示">七大核心功能（含 GIF）</a> ·
  <a href="docs/chaos-network-lab.md">Chaos Mesh 故障注入排障实验</a> ·
  <a href="#四故障注入排障实战与内置文档">文档与性能评估</a>
</p>

---

## 一、项目功能简介

**calico-plumber** 是一款专为 Kubernetes **Calico** 网络打造的容器化 Web 诊断与可观测性平台，帮助运维与平台工程师把分散在 `calicoctl`、`birdcl`、`tcpdump`、`conntrack`、`iptables/ipvsadm` 中的排障工作整合到一个可视化控制台中：

- **全模式网络感知**：自动识别 Calico **BGP 直连**、**IPIP**（`Always` / `CrossSubnet`）、**VXLAN**（`Always` / `CrossSubnet`）及 **kube-proxy**（`iptables` / `IPVS` / `nftables` / Calico eBPF）模式，直连节点 `/var/run/calico/bird.ctl` 实时采集 BIRD BGP 邻居状态。
- **自适应双引擎流量遥测**：
  - **模式一 — `eBPF`**：在支持 BTF 的现代内核（Linux 5.8+ 或 RHEL / Rocky 8/9 等）上自动加载 CO-RE eBPF 探针，零侵入获取五元组流量、吞吐速率、内核 `srtt_us` 连接延迟及 TCP 重传 / RST 统计。
  - **模式二 — `Legacy`**：在不具备 BTF/eBPF 条件的老内核节点上自动平滑降级为 **Netlink Conntrack + Pod Netns `INET_DIAG` (`tcp_info`)** 混合采集，同一集群内异构内核节点无缝混部。
- **零侵入单 Pod 深度排障与分层 TraceFlow**：Node Agent 通过宿主机进入目标 Pod 的 Network Namespace，**无需业务容器预装 `tcpdump`、`curl`、`nc` 或 `sh`**（完美兼容 `distroless` / `scratch` 镜像），支持实时抓包（`.pcap` 下载）、TCP 三次握手时序检测、HTTP/HTTPS 瀑布流探测，以及按「Node → 物理网卡 → OS 内核/用户态 → 虚拟交换/隧道 → 容器」垂直分层绘制并支持逐跳回放的多跳 TraceFlow。
- **网络体检与一致性审计**：从 Calico 网络模式与 MTU 封装链路、Path MTU / DF 大包黑洞探测、BIRD BGP 邻居与 Expected vs Actual 三层路由一致性比对、二层 MAC/网关 ARP/Proxy ARP 邻居表，到 NAT 转发异常与底层 Conntrack / Sysctl 证据逐层排查。
- **按症状排障与证据驱动下一步**：`Pod 排障` 默认提供业务症状入口（访问服务失败 / 偶发超时或变慢 / 只有跨节点访问失败），选定源工作负载与目标 Service 自动带入端口，也可一键切换专家工具（TCPdump / TCP / HTTP）；异常 Service 详情依据证据动态给出唯一最有价值的下一步（查看后端为何为空、直接读取 Pod Netns `/proc/net/tcp` 核对真实监听端口、追踪 Service 转发，或对比巡检探针与业务 Pod 路径并区分展示 `实际观测到` 与 `按配置推算` 的 Calico/K8s 策略）。
- **异常优先拓扑与无损上下文返回**：`流量拓扑` 默认以 **「看异常连接」** 呈现（支持随时切换「全景视图」），左侧按重传同比基线变化、RST 与延迟排序，右侧仅展开选中连接及其上下游一跳；点进 TraceFlow 自动沿用五元组与时间窗，支持一键返回原拓扑视图。
- **全局健康逐端口审计与偶发故障现场复测**：首页置顶可点击的一句话巡检判定，明确区分「跨节点双向连通」与「探针自身检查」，逐端口区分 `正常 / 连接被拒绝 / 等待超时 / 未测`；自动捕获并留存故障现场及周边 Pod/Endpoint/策略/BGP 变更时间轴，修复后一键 **「用相同条件复测」** 直观对比前后差异。
- **一键导出故障现场诊断包**：顶部导航栏、审计页与 TraceFlow 结论栏支持一键导出 `.zip` 诊断包（含零外链离线 `report.html` 与 `01..08-*.json` 全量结构化现场与故障复测证据），方便跨团队工单流转与复盘。
- **全维度 IPAM & 资产检索**：深度解析 `IPPool`、`IPAMBlock`、`BlockAffinity` CRD，提供节点级 IP 位图热力图（活跃 Pod / 隧道 IP / 跨节点借用 / 孤儿残留 / 空闲）与 `⌘K` 全局检索。
- **内置 Hugo 技术文档**：Web 控制台右上角一键直达内置静态文档站（`/docs/`），涵盖实现原理、网络/性能影响评估与常见实战排障教程。

### 系统架构

```mermaid
flowchart TB
    User["浏览器 Web 控制台 / 内置 Hugo 文档 (/docs/)"]

    subgraph CP["控制面 Deployment (calico-netdiag-server)"]
        API["REST / WebSocket API + 嵌入式 React SPA & Hugo Docs"]
        Store["K8s & Calico CRD Store\n(IPPool / IPAMBlock / BGP / Pod / Service)"]
        FlowAgg["全局流量聚合与拓扑引擎"]
        Orch["多跳 TraceFlow 编排器 & 全局健康巡检器"]
    end

    subgraph Node["各集群节点 DaemonSet"]
        subgraph Agent["calico-netdiag-agent (hostNetwork / hostPID)"]
            Engine["自适应双引擎采集\neBPF (CO-RE) / Legacy (Conntrack + INET_DIAG)"]
            Netns["零侵入 Pod Netns 诊断器\nTraceFlow / TCPdump / TCP 握手 / HTTP 探测"]
            DP["Calico 数据面探测器\nBIRD Socket / Route / iptables / IPVS"]
        end
        Probe["calico-netdiag-probe\n(每节点 Pod 级连通性轻量探针)"]
    end

    User -->|HTTP / WebSocket| API
    API --> Store
    API --> FlowAgg
    API --> Orch
    Store -->|Read-Only RBAC| K8sAPI[("Kubernetes API Server")]
    Engine -->|定时上报五元组/吞吐/RTT| FlowAgg
    Orch <-->|HTTP / WS 协同抓包与探测| Netns
    Orch <-->|Pod-Pod / SVC 探测| Probe
    DP -->|节点 BGP/数据面状态| API
```

---

## 二、快速部署方式

### 前置要求

- Kubernetes `v1.22+` 集群（使用 Calico CNI，支持 Operator 或 Manifests 安装方式）
- 镜像架构：`linux/amd64`（公开镜像已托管于 Docker Hub `docker.io/lammw12`）
- 内核兼容性：
  - 支持 `/sys/kernel/btf/vmlinux` 的节点将自动运行 **`eBPF`** 模式；
  - 无 BTF 的旧内核节点将自动运行 **`Legacy`** 模式，无需额外手工配置。

### 方式 1：单文件一键部署（推荐）

直接使用包含命名空间、只读 RBAC、Server、Agent DaemonSet 与 Probe DaemonSet 的整合清单（拉取 Docker Hub 公开镜像）：

```bash
kubectl apply -f https://raw.githubusercontent.com/sdnmw/calico-plumber/main/deploy/k8s-dockerhub.yaml
```

检查 Pod 就绪状态：

```bash
kubectl -n calico-netdiag get pods -o wide
```

期望输出所有节点上的 `calico-netdiag-agent`、`calico-netdiag-probe` 以及 `calico-netdiag-server` 均为 `Running (1/1)`。

### 方式 2：克隆仓库分模块部署

```bash
git clone https://github.com/sdnmw/calico-plumber.git
cd calico-plumber
kubectl apply -f deploy/k8s/
```

### 访问 Web 控制台与内置文档

1. **NodePort 访问（默认已开启 `31880`）**：
   - Web 控制台：`http://<任意集群节点IP>:31880`
   - 内置技术文档：`http://<任意集群节点IP>:31880/docs/`

2. **本地 `kubectl port-forward` 访问**：
   ```bash
   kubectl -n calico-netdiag port-forward svc/calico-netdiag-server 8080:8080
   # 浏览器访问 http://localhost:8080
   ```

3. **通过 Ingress 绑定自定义域名**：
   可参考模板 [deploy/k8s/05-ingress-optional.yaml.example](deploy/k8s/05-ingress-optional.yaml.example)，修改 `ingressClassName` 与 `host` 后应用即可。
   > **提示**：实时 TCPdump 使用 WebSocket（`/api/v1/diag/tcpdump/ws`），若使用 NGINX / Envoy 等 Ingress Controller，请确保开启 WebSocket 透传并适当调大读写超时（如 `600s`）。

### 切换流量采集引擎模式

在 `deploy/k8s/03-agent-daemonset.yaml`（或 `deploy/k8s-dockerhub.yaml`）中修改 Agent 容器的 `TELEMETRY_MODE` 环境变量即可按需切换：

| 取值 | 行为说明 |
| :--- | :--- |
| `auto`（默认） | 自动探测宿主机 BTF 与 eBPF 条件：满足则启用 **`eBPF`**，否则自动降级为 **`Legacy`** |
| `ebpf` | 优先启用 **`eBPF`** 模式，加载失败时自动回退 **`Legacy`** |
| `conntrack` | 强制所有节点使用 **`Legacy`**（Netlink Conntrack + Pod Netns `INET_DIAG`）模式 |

```bash
# 示例：临时切换全集群 Agent 为 Legacy 模式验证
kubectl -n calico-netdiag set env ds/calico-netdiag-agent TELEMETRY_MODE=conntrack
# 恢复自适应模式
kubectl -n calico-netdiag set env ds/calico-netdiag-agent TELEMETRY_MODE=auto
```

### 从源码自行构建镜像

项目所有组件均采用多阶段 Dockerfile（`Dockerfile.server` 内置完成 React UI 构建、Hugo 文档生成与 Go 静态嵌入编译）：

```bash
# 运行 Go 单元测试
make test

# 构建并推送 3 个组件镜像至自定义仓库（默认 REGISTRY=docker.io/lammw12）
make build-push REGISTRY=<your-registry>/<project> TAG=v0.1.0
```

> 若在受限网络环境构建，可通过 `--build-arg GOPROXY=https://goproxy.cn,direct` 及 `GO_IMAGE` / `NODE_IMAGE` / `ALPINE_IMAGE` 参数替换为本地镜像加速器。

### 卸载清理

```bash
kubectl delete -f https://raw.githubusercontent.com/sdnmw/calico-plumber/main/deploy/k8s-dockerhub.yaml --ignore-not-found
```

---

## 三、七大核心功能与演示

下面通过 7 段在线集群实录 GIF，快速了解 **calico-plumber** 的七大核心模块。

### 1. 集群总览（Cluster Overview）

进入控制台即可一屏掌控整个集群的 Calico 网络画像：
- 顶部以简洁卡片呈现 Kubernetes / Calico 版本，自动识别 **Calico 封装模式**（BGP / IPIP / VXLAN / CrossSubnet）与 **`kube-proxy` 数据面模式**（iptables / IPVS / nftables）；
- 展示 **实时活跃五元组数**、**平均连接延迟（RTT）** 与 **各节点遥测引擎分布**（`eBPF` vs `Legacy`）；
- 实时列出每个节点的 **BIRD BGP Peer 会话状态**（带 BGP Enabled / AS / Mesh 状态标签）、内核版本、网卡拓扑与 IPPool 使用率，支持明暗主题切换、一键导出诊断包与右上角打开内置技术文档。

![01-集群总览](docs/assets/01-overview.gif)

---

### 2. 全局流量拓扑（Live Traffic Topology）

将集群内实时活跃网络流渲染为可交互的有向拓扑图，快速发现异常流量、跨节点热点与高延迟链路：
- 支持按 **Namespace / Workload / Pod / Node** 四个维度实时切换聚合粒度，并按关键词筛选 IP、端口或服务名；
- 底部 **实时五元组流表** 支持鼠标悬停截断端点名即时显示完整 Pod/Service 名称与 `IP:Port`；
- 连线实时标注吞吐速率与延迟，点击任意流量边即可展开 **五元组明细抽屉** 并一键发起 **TraceFlow** 或 **TCPdump**。

![02-全局流量拓扑](docs/assets/02-topology.gif)

---

### 3. IPAM 全景与全局检索（IPAM Heatmap & Omni-Search）

彻底告别手工翻查 Calico `IPAMBlock` 与 `BlockAffinity` 的繁琐过程：
- **IPPool 与各节点 Block 占用双览**：顶部并排展示 `IPPool` 总水位与 **各节点 IPAM Block 占用卡片**（逐节点展示 Block 数、占用进度条及各 CIDR 槽位利用率）；
- **节点级 IPAM 位图热力图**：直观区分每个 IP 槽位状态——**活跃 Pod**（绿）、**隧道 IP**（紫）、**跨节点借用**（黄）、**孤儿残留**（红）与 **空闲**（灰），点击任意已分配格即可侧滑查看 **Pod 数据面详情抽屉**（`cali*` veth、路由表、NetworkPolicy）；
- **`⌘K` 全维度全局搜索**：在任意页面按 **Pod IP / ClusterIP / Pod / Deployment / Service / Node / 标签** 毫秒级定位排障目标。

![03-IPAM 与全局搜索](docs/assets/03-ipam-search.gif)

---

### 4. 多跳路径追踪（Live TraceFlow）

针对“同一集群内不通 / 偶发超时 / Service 转发异常 / 外部 SNAT”等疑难场景，提供真实报文级别的端到端多跳观测：
- 默认提供 3 个 **一键快速场景**（跨节点 Pod → Pod、Pod → Service DNAT、Pod → 节点 ICMP），支持 TCP / UDP / ICMP 协议；
- 按真实宿主机软件栈垂直分层绘制（**Node 底层 → 物理网卡 → OS 内核态/用户态 → 虚拟交换/隧道 → 容器 `eth0`**），并支持 **逐跳数据流动效回放**，清晰呈现源节点下行 → 物理网络 → 目标节点上行的真实转发路径；
- 自动解包 IPIP / VXLAN 外层隧道头、识别 Calico `MASQUERADE --random-fully` SNAT 与 `hostNetwork` Service，结论栏支持一键 **导出本次诊断包**。

![04-多跳路径追踪](docs/assets/04-traceflow.gif)

---

### 5. 单 Pod 零侵入排障工作台（Pod Troubleshoot Suite）

无需向业务容器注入 debug 边车或预装任何网络工具，选定任意 Pod 即可执行三大深度诊断：
- **TCPdump 实时抓包**：支持选择 Pod `eth0` 或宿主机 `cali*` / `tunl0` / `vxlan.calico` / 物理网卡，支持标准 BPF 过滤语法、WebSocket 实时流式输出、点击单帧查看 **Hex / ASCII 原始载荷** 以及抓包结束后一键下载 **`.pcap` 文件** 供 Wireshark 分析；
- **TCP 三次握手检测**：在 Pod Netns 内精确观测 `SYN → SYN-ACK → ACK`（或 `RST` / 超时）完整时序与微秒级 RTT，清晰定位建连拒绝、防火墙丢弃或中间链路抖动；
- **HTTP / HTTPS 瀑布流探测**：输出 **DNS 解析 → TCP 建连 → TLS 握手 → TTFB 首字节 → 内容传输** 逐阶段耗时瀑布图、响应状态码与自签名证书跳过选项。

![05-单 Pod 零侵入排障](docs/assets/05-pod-troubleshoot.gif)

---

### 6. 路由 / ARP 状态与诊断包导出（Route / ARP Audit & Bundle）

面向 Calico 生产运维最常见的隐蔽故障，按网络平面逐层体检并支持一键留档：
- **网络模式与 MTU 封装链路 + Path MTU DF 探测**：直观展示当前封装开销（BGP `0B` / IPIP `20B` / VXLAN `50B`）与物理 NIC → 隧道 → 容器 veth MTU 对齐状态，内置 Pod Netns **DF 大包黑洞二分探测**；
- **网络异常体检结果（三要素呈现）**：以网络/业务语言简洁给出 **体检结果、根因预测、检测方法**，需要时可展开底层 **Conntrack / Sysctl 内核证据**；
- **Calico BGP 邻居与 Expected vs Actual 路由一致性审计**：自动比对期望路由与各节点 Linux 主路由表，检出跨节点 Block 缺失、幽灵 Pod `/32` 残留、非对称路径与 BGP 状态翻转；
- **二层 MAC 与 ARP 邻居表**：实时采集各节点物理网卡 MAC、默认网关 ARP、Calico `cali*` Proxy ARP 开关及 IPv4 邻居表，高亮 `FAILED` 条目；
- **一键导出故障现场诊断包（`.zip`）**：内含零外链离线 `report.html` 与全量结构化 JSON 证据。

![07-路由与ARP状态审计](docs/assets/07-route-arp-audit.gif)

---

### 7. 全局网络健康检查（Global Health Audit）

用于变更前基准验证、变更后回归巡检及日常网络 SLA 监控：
- **全节点 Pod-Pod N×N 延迟矩阵**：基于每节点轻量 Probe Pod 实测同节点与跨节点连通性及延迟，点击矩阵中任意单元格可直接带参跳转发起 **TraceFlow**；
- **基础设施连通性检查**：自动验证 Pod-Node、Pod-SVC（Kubernetes API / CoreDNS）以及 Calico BGP 会话、MTU 一致性与 IPAM 水位；
- **全集群 Service 后端穿透审计（异常一键跳转）**：点击顶部 **「Service 后端健康」** 卡片（或其 `异常 / 降级 / 无后端` 计数）自动滚动定位至审计表、筛选 **仅异常** 并展开首条故障 Service 的后端 `TCP FAIL` 明细。

![06-全局网络健康检查](docs/assets/06-global-health.gif)

---

## 四、故障注入排障实战与内置文档

### Chaos Mesh 容器网络故障注入实战手册

仓库在 [`docs/`](docs/chaos-network-lab.md) 目录提供了一套完整的 **Chaos Mesh + Kubernetes/Calico 故障注入与排障实验手册**（附可一键部署的靶场与故障 YAML 清单及 **Chaos Mesh Web UI + calico-plumber Web UI 双视角实机截图**）：

- 📖 完整实验指南：[**docs/chaos-network-lab.md**](docs/chaos-network-lab.md)
- 🧪 实验覆盖 6 大经典故障：**跨节点网络高延迟与抖动**（`NetworkChaos: delay`）、**35% 高丢包与 TCP 重传风暴**（`NetworkChaos: loss`）、**跨节点双向网络分区**（`NetworkChaos: partition`）、**NetworkPolicy 误配精准端口阻断**、**Service `targetPort` 错配秒回 `RST`**，以及 **外部旧内核 `tcp_tw_recycle=1` + Calico SNAT 引发的“Pod 能通、同节点 Node 不通（PAWS 丢包）”**。

### 内置技术文档与性能说明

点击 Web 控制台右上角 **「文档」** 按钮即可访问内置的 Hugo 技术文档站（`/docs/`），包含三篇深度文档：

1. **核心实现原理**（[docs-hugo/content/principles.md](docs-hugo/content/principles.md)）：详述 Calico 模式识别、BIRD Unix Socket 通信、`eBPF` CO-RE vs `Legacy` 采集管线、多节点 TraceFlow 协同与 Netns 零侵入抓包原理。
2. **性能与网络影响评估**（[docs-hugo/content/performance.md](docs-hugo/content/performance.md)）：对 CPU、内存、带宽与业务转发延迟的量化评估。
3. **常用使用教程与实战**（[docs-hugo/content/tutorials.md](docs-hugo/content/tutorials.md)）：常见排障场景端到端 SOP。

### 性能与安全边界摘要

| 维度 | `eBPF` 模式（默认现代内核） | `Legacy` 模式（旧内核自动降级） |
| :--- | :--- | :--- |
| **业务转发路径侵入** | **零旁路拦截**（仅只读 kprobe 统计，不修改/丢弃数据包） | **零旁路拦截**（仅定时读取内核 Conntrack 表与 `INET_DIAG`） |
| **单节点典型 CPU 开销** | 约 `10m – 40m`（硬限制 `500m`） | 约 `20m – 80m`（硬限制 `500m`） |
| **单节点典型内存占用** | 约 `35Mi – 80Mi`（硬限制 `256Mi`） | 约 `40Mi – 95Mi`（硬限制 `256Mi`） |
| **K8s RBAC 权限** | Server 仅需 **只读**（`get`/`list`/`watch`）权限，不修改任何 K8s / Calico 资源 | 同左 |

---

## 五、项目目录结构

```text
calico-plumber/
├── cmd/
│   ├── server/          # 控制面主程序（聚合 API、TraceFlow 编排、嵌入 Web UI 与 Hugo Docs）
│   ├── agent/           # 节点 DaemonSet Agent（eBPF/Legacy 遥测、Netns 排障、BIRD/Dataplane 探测）
│   └── probe/           # 轻量级每节点连通性探针 DaemonSet
├── pkg/
│   ├── model/           # 共享数据模型（Cluster / IPAM / Flow / TraceFlow / Diag / Health）
│   ├── k8s/             # K8s & Calico CRD Informer、IPAM 位图分类器、Omni-Search 索引
│   ├── agent/           # Netns 切入、BIRD/Route/IPVS 探测、eBPF CO-RE & Legacy 采集引擎、排障执行器
│   └── server/          # 流量聚合环、Agent 客户端、TraceFlow 拼接器、全局健康体检器、REST/WS API
├── web/                 # React 19 + TypeScript + Vite + Tailwind + React Flow 前端工程
├── docs-hugo/           # Hugo 静态技术文档源码（嵌入至 /docs/）
├── docs/
│   ├── assets/          # README 6 大功能演示 GIF 动图
│   ├── chaos-network-lab.md  # Chaos Mesh + Calico 六大经典网络故障注入与排障实战手册
│   └── lab/             # 故障注入实验 YAML 清单与 Chaos Mesh / calico-plumber 双界面截图
├── deploy/
│   ├── docker/          # 多阶段 Dockerfile（Server / Agent / Probe）
│   ├── k8s/             # 分模块 Kubernetes 部署清单与 Ingress 模板
│   └── k8s-dockerhub.yaml  # 单文件一键部署清单
├── Makefile
├── README.md            # 英文 README（默认）
├── README_ZH.md         # 简体中文 README
└── LICENSE              # Apache License 2.0
```

---

## License

本项目采用 [Apache License 2.0](LICENSE) 协议开源。
