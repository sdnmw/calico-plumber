# 容器网络故障注入与 calico-plumber 排障实战手册

本手册设计了一套可复现的 **Kubernetes + Calico 跨节点容器网络故障注入与诊断实验室**，结合 **Chaos Mesh**（`NetworkChaos`）与真实生产常见的 **NetworkPolicy / Service 误配场景**，演示如何通过 **calico-plumber** 在分钟级完成从“现象发现 → 路径定界 → 报文取证 → 根因定位 → 恢复验证”的全流程排障。

> **实验覆盖的六大经典网络故障模式**：
> 1. **实验 1 — 跨节点网络高延迟与抖动**（Chaos Mesh `NetworkChaos: delay`）→ `流量拓扑 + HTTP 瀑布流`
> 2. **实验 2 — 高丢包率与 TCP 重传风暴**（Chaos Mesh `NetworkChaos: loss`）→ `流量拓扑重传标红 + Pod 实时 TCPdump 抓包`
> 3. **实验 3 — 跨节点双向网络分区**（Chaos Mesh `NetworkChaos: partition`）→ `多跳 TraceFlow + TCP 三次握手 SYN_TIMEOUT`
> 4. **实验 4 — NetworkPolicy 误配导致精准端口阻断**（`Ingress Policy Drop`）→ `IPAM Pod 检查抽屉 + TraceFlow 策略链定位`
> 5. **实验 5 — Service `targetPort` 错配（应用端口未监听）**（`Connection Refused`）→ `全局健康 Service 穿透审计 + TCP 三次握手秒回 RST`
> 6. **实验 6 — 外部旧内核服务端 `tcp_tw_recycle=1` + Calico SNAT 引发“Pod 能通、同节点 Node 不通”**（`PAWS SYN Drop`）→ `流量拓扑同框对照 + TraceFlow + TCP 三次握手对比 + 宿主机 ens4 TCPdump 抓包 + 对端 PAWSPassive 取证`

---

## 一、实验环境与靶场架构

### 1.1 拓扑说明

为了还原真实生产中的跨节点 Calico 转发路径，我们在隔离命名空间 `calico-plumber-lab` 中部署一组双层 HTTP 微服务，并通过 `podAntiAffinity` 将两端调度在不同节点上：

```mermaid
flowchart LR
    subgraph NodeA["节点 A (Control-Plane / Node-1)"]
        Client["lab-client Pod\n(持续每 0.4s 请求 backend)"]
        VethA["cali* veth"]
    end

    subgraph Fabric["Calico BGP / 底层网络"]
        Net(("Cross-Node Fabric"))
    end

    subgraph NodeB["节点 B (Worker / Node-2)"]
        VethB["cali* veth"]
        SVC["Service: lab-backend\nClusterIP :8080"]
        Backend["lab-backend Pod\n(HTTP Server :8080)"]
    end

    Chaos["Chaos Mesh\n(NetworkChaos Dashboard UI)"] -.->|"tc netem / iptables 注入"| Backend
    Client --> VethA --> Net --> VethB --> SVC --> Backend
```

### 1.2 前置部署

1. **部署 Chaos Mesh（含 Web Dashboard UI，适配 `containerd` 运行时）**：
   ```bash
   helm repo add chaos-mesh https://charts.chaos-mesh.org
   helm repo update

   helm upgrade --install chaos-mesh chaos-mesh/chaos-mesh \
     -n chaos-mesh --create-namespace \
     --set chaosDaemon.runtime=containerd \
     --set chaosDaemon.socketPath=/run/containerd/containerd.sock \
     --set dashboard.securityMode=false \
     --set dashboard.service.type=NodePort
   ```
   > 若测试集群控制面节点存在 `NoSchedule` 污点且需要对其上的 Pod 注入故障，请额外追加 `--set "chaosDaemon.tolerations[0].operator=Exists"`。

2. **部署跨节点实验靶场（[lab/00-target-app.yaml](lab/00-target-app.yaml)）**：
   ```bash
   kubectl apply -f docs/lab/00-target-app.yaml
   kubectl -n calico-plumber-lab get pods,svc -o wide
   ```

### 1.3 正常基线观测（Baseline）

故障注入前，在 **calico-plumber** 中打开 **「流量拓扑」**，筛选命名空间 `calico-plumber-lab`、粒度切换为 **`Pod`**、时间窗选择 **`最近 1m`**，点击跨节点连线即可看到健康基线：
- **跨节点 RTT 均值**：`0.66 ms`（P95 `3.52 ms`）
- **TCP 重传 / RST**：`0 / 0`
- **健康状态**：绿色 `healthy`（eBPF CO-RE 实时采集）

![00-正常基线流量拓扑](lab/screenshots/00-baseline-topology.png)

---

## 二、六大经典故障注入与排障实录

### 实验 1：跨节点网络高延迟与抖动（Chaos Mesh `delay: 250ms ± 50ms`）

#### 故障背景与注入

模拟跨机柜/跨可用区链路拥塞或底层网卡排队导致的接口响应变慢，但业务日志无报错、Pod 探针全部 `Ready`。

```bash
kubectl apply -f docs/lab/01-chaos-delay.yaml
```

> **工程细节**：`01-chaos-delay.yaml` 选择 `selector: app=lab-backend` 且 `direction: to`、`target: app=lab-client`。原因在于 `lab-client` 通过 Service ClusterIP 访问 `lab-backend`，经过宿主机 `kube-proxy` DNAT 后到达 `lab-backend` 时源 IP 仍保留为 `lab-client` 的真实 Pod IP；因此该配置同时覆盖 **Service 与直连 Pod 流量**，且不会误伤 `kubelet` 的节点本地探针（`lab-backend` 保持 `1/1 Ready`）。

在 **Chaos Mesh Dashboard Web UI** 中确认实验进入 `Running` 状态，事件时间轴显示已成功注入目标 Pod，`Definition` 面板清晰展示 `latency: 250ms` / `jitter: 50ms`：

![01-Chaos Mesh UI 延迟注入](lab/screenshots/01-chaos-delay-ui.png)

#### 使用 calico-plumber 诊断排障

1. **第一步 — 流量拓扑秒级定位慢链路**：
   切回 **calico-plumber「流量拓扑」**（`calico-plumber-lab` / `最近 1m`），`lab-client → lab-backend` 跨节点连线立刻标红告警（`critical`）：
   - 实时延迟从基线 `0.66 ms` 暴涨至 **`248.3 ms`（P95 `291.4 ms`）**，重传为 `0`；
   - 同画布下方节点本地探针 `mw-workergroup1 → lab-backend` 延迟仍为 `<0.1 ms`，**一秒排除节点内业务进程卡死**，锁定为跨节点网络延迟。

   ![01-calico-plumber 拓扑延迟告警](lab/screenshots/01-plumber-delay-topology.png)

2. **第二步 — Pod 排障 HTTP 瀑布流拆分耗时阶段**：
   点击连线进入 **「Pod 排障 → HTTP / HTTPS 瀑布流探测」**，以 `lab-client` 视角发起 `GET http://lab-backend.calico-plumber-lab.svc:8080/api/ping`：
   - **DNS 解析**：`2.39 ms`（排除 CoreDNS 慢查询）
   - **TCP 建连**：`214.05 ms`（1 次往返即叠加 ~250ms 延迟）
   - **TTFB 等待**：`275.89 ms`（HTTP 请求/响应再叠加 1 次 ~250ms 延迟）
   - **总耗时**：`507.14 ms`（`200 OK`），精准证实根因为传输层往返延迟（RTT）翻倍。

   ![01-calico-plumber HTTP 瀑布流](lab/screenshots/01-plumber-delay-waterfall.png)

清理实验 1：
```bash
kubectl delete -f docs/lab/01-chaos-delay.yaml
```

---

### 实验 2：高丢包率与 TCP 重传风暴（Chaos Mesh `loss: 35%`）

#### 故障背景与注入

模拟光模块劣化、交换机端口 CRC 错包或宿主机软中断溢出引起的随机丢包：业务表现“大部分请求几毫秒完成，偶发毛刺飙升到 1 秒以上甚至请求超时”。

```bash
kubectl apply -f docs/lab/02-chaos-loss.yaml
```

在 **Chaos Mesh Dashboard Web UI** 确认 `lab-exp2-packet-loss` 处于 `Running` 状态（`action: loss`、`loss: 35%`、`correlation: 25%`）：

![02-Chaos Mesh UI 丢包注入](lab/screenshots/02-chaos-loss-ui.png)

#### 使用 calico-plumber 诊断排障

1. **第一步 — 流量拓扑捕获 TCP 重传尖峰**：
   在 **calico-plumber「流量拓扑」**中，`lab-client → lab-backend` 跨节点边立刻变为 `critical` 告警；展开右侧五元组明细可见：
   - **平均 RTT 依然极低（`0.69 ms`）**（与实验 1 的“高延迟零重传”形成鲜明对比！）；
   - 但 **TCP 重传数在 1 分钟内飙升至 `25`**，多条 eBPF 五元组连接被标为 `warning`，直接指向丢包而非链路拥塞延迟。

   ![02-calico-plumber 拓扑捕获重传](lab/screenshots/02-plumber-loss-topology.png)

2. **第二步 — 零侵入 Pod TCPdump 抓包取证**：
   点击右侧明细中的 **「抓包」** 进入 **「Pod 排障 → TCPdump 实时抓包」**（无需 `lab-client` 容器预装 `tcpdump`），设置 BPF 过滤 `tcp port 8080` 并点击开始：
   - 实时流清晰记录了乱序与重传全过程：
     - **Frame #10**：`8080 → 46344 [ACK,PSH,FIN] Seq=2253218109`（后续分段先到，前序 HTTP Header 分段被丢弃）
     - **Frame #11**：`46344 → 8080 [ACK] Ack=2253217963`（客户端发送重复 ACK 请求补发缺失序列号）
     - **Frame #12**：`8080 → 46344 [ACK,PSH] Seq=2253217963 Len=146`（服务端重传丢失的 HTTP Header 分段）
   - 停止抓包后可点击单帧查看 Hex / ASCII 载荷或一键导出 `.pcap` 交由 Wireshark 留档。

   ![02-calico-plumber 实时 TCPdump 抓包](lab/screenshots/02-plumber-loss-tcpdump.png)

清理实验 2：
```bash
kubectl delete -f docs/lab/02-chaos-loss.yaml
```

---

### 实验 3：跨节点双向网络分区（Chaos Mesh `partition: both`）

#### 故障背景与注入

模拟脑裂、底层防火墙规则误拦截或主机侧规则阻断导致的两个微服务之间完全断联，客户端全部报 `i/o timeout`。

```bash
kubectl apply -f docs/lab/03-chaos-partition.yaml
```

在 **Chaos Mesh Dashboard Web UI** 确认 `lab-exp3-network-partition` 已在 `lab-client` 与 `lab-backend` 上生效（`action: partition`、`direction: both`）：

![03-Chaos Mesh UI 网络分区](lab/screenshots/03-chaos-partition-ui.png)

#### 使用 calico-plumber 诊断排障

1. **第一步 — 多跳 TraceFlow 精准定界丢包位置**：
   打开 **「TraceFlow」**，选择源 Pod `calico-plumber-lab/lab-client` → 目标 Pod `calico-plumber-lab/lab-backend`（`TCP:8080`），点击 **「执行 Trace」**：
   - 结果立刻返回 **`DROPPED · BGP · 跨节点 (Cross-Node)`**；
   - 逐跳泳道清晰指出：**`Kernel Route Lookup`（宿主机 BGP 路由）为 `PASSED`**，但报文在 **`Source Pod eth0` 即已被拦截未发出至宿主机 `cali*` veth** —— 这正是 Chaos Mesh 在 Pod Netns 内插入 `CHAOS-OUTPUT` iptables `DROP` 链的真实内核行为！

   ![03-calico-plumber TraceFlow 定位源端阻断](lab/screenshots/03-plumber-partition-traceflow.png)

2. **第二步 — TCP 三次握手确认“静默丢包（`SYN_TIMEOUT`）”**：
   进入 **「Pod 排障 → TCP 三次握手检测」**，探测 `lab-backend.calico-plumber-lab.svc:8080`：
   - **DNS 解析正常**（`2.54 ms` 解析出 ClusterIP `10.250.255.178`）；
   - 随后发出的 `SYN` 在 `+2.54ms`、`+1066.47ms` 连续重传，始终未收到 `SYN-ACK` 或 `RST`，`3000ms` 后诊断结论明确输出 **`SYN_TIMEOUT`** —— 铁证表明中间存在静默丢包（防火墙 / 网络策略 / 网络分区），而非对端端口未监听。

   ![03-calico-plumber TCP 握手 SYN_TIMEOUT](lab/screenshots/03-plumber-partition-handshake.png)

清理实验 3：
```bash
kubectl delete -f docs/lab/03-chaos-partition.yaml
```

---

### 实验 4：NetworkPolicy 误配导致的精准端口阻断（Calico Ingress Policy Drop）

#### 故障背景与注入

这是 Kubernetes + Calico 生产环境中**最高发**的故障之一：Pod 全是 `Running`、Service Endpoint 完整、BGP 路由正常，但安全基线或发布人员在配置 `NetworkPolicy` 时将允许端口误写成 `8443`（实际服务监听 `8080`），导致 `lab-client → lab-backend:8080` 被目标节点 Calico Felix 规则静默丢弃。

```bash
kubectl apply -f docs/lab/04-networkpolicy-drop.yaml
```

#### 使用 calico-plumber 诊断排障

1. **第一步 — IPAM & Pod 深度检查抽屉直查生效策略**：
   打开 **「IPAM 与搜索」**，搜索 `lab-backend` 并点击 Pod 名称滑出右侧 **Pod 检查抽屉**，直接暴露 `lab-backend` 的 `cali*` veth、IPAM Block、内部路由表以及当前命中的 **`NetworkPolicy: lab-exp4-deny-wrong-port`**！

   ![04-calico-plumber IPAM Pod 检查抽屉](lab/screenshots/04-plumber-netpol-drawer.png)

2. **第二步 — 多跳 TraceFlow 精确标红目标节点 `Calico Ingress Policy` 与底层链名**：
   执行 `lab-client → lab-backend:8080` 实时 **TraceFlow**，可以看到与实验 3 截然不同的跨节点逐跳路径：
   - 源节点 `eth0 → cali* veth → Egress Policy → Route → ens4` 全部 **`OBSERVED / PASSED`**；
   - 底层 BGP 转发正常，**目标节点物理网卡 `Dest Host NIC Ingress (ens4)` 已实测收到报文（`+1.14ms`）**；
   - 报文在 **`Hop #8: Calico Ingress Policy (cali-tw-cali*)`** 精准标红 **`DROPPED`**，底部详情栏直接打印出命中的策略名称与底层 iptables 链：
     `Matched 1 policy(s): lab-exp4-deny-wrong-port | Ingress(cali-tw-cali213c861f56f): DROP`！

   ![04-calico-plumber TraceFlow 定位 Calico Ingress Policy Drop](lab/screenshots/04-plumber-netpol-traceflow.png)

清理实验 4：
```bash
kubectl delete -f docs/lab/04-networkpolicy-drop.yaml
```

---

### 实验 5：Service `targetPort` 错配（应用端口未监听 → 秒回 `RST`）

#### 故障背景与注入

模拟上线变更误将 Service `targetPort` 从 `8080` 改为未监听的 `9999`（或业务进程崩溃未监听端口），客户端瞬间报错 `Connection refused`。

```bash
kubectl apply -f docs/lab/05-service-port-mismatch.yaml
```

#### 使用 calico-plumber 诊断排障

1. **第一步 — 全局健康体检中心一键揪出异常 Service 与后端端口**：
   打开 **「全局健康」** 点击 **「立即全量巡检」**，在底部 **全集群 Service & 后端 Endpoint 健康审计** 表格中搜索 `lab-backend` 并展开明细：
   - `calico-plumber-lab/lab-backend` 被精准标红为 **`broken`**；
   - **`ClusterIP 探测: FAIL`**，展开后的后端探测明细直接指出 **`172.31.226.14:9999 → TCP FAIL`**，清晰区分“kube-proxy 规则故障”与“后端目标端口本身不可达”。

   ![05-calico-plumber 全局健康审计标红 broken Service](lab/screenshots/05-plumber-rst-health.png)

2. **第二步 — TCP 三次握手毫秒级捕获 `RST_REFUSED`**：
   在 **「Pod 排障 → TCP 三次握手检测」** 中从 `lab-client` 探测 `lab-backend.calico-plumber-lab.svc:8080`：
   - 仅耗时 **`0.77 ms`** 即收到对端内核返回的 **`[RST] Connection refused`**（`Δ 0.48ms`）；
   - 报告直接给出明确结论：**`网络路由与策略已通，但目标端口未监听或应用拒绝连接`**，彻底避免在防火墙/Calico 路由上浪费排障时间。

   ![05-calico-plumber TCP 握手捕获 RST_REFUSED](lab/screenshots/05-plumber-rst-handshake.png)

---

### 实验 6：外部旧内核服务端 `tcp_tw_recycle=1` + Calico SNAT 引发“Pod 能通、同节点 Node 不通”

#### 故障背景与内核原理

这是 Kubernetes 生产环境中最令人抓狂的“玄学故障”之一：**集群中某台 Worker 节点上的业务 Pod 能丝滑访问集群外部的传统 Web / 数据库服务（`200 OK`, `< 1ms`），但登录到同一台宿主机 Node 直接 `curl` 同一个外部地址却 100% 超时不通（有时同节点的其他 Pod 也会间歇或持续不通）！**

其根因是 **Calico `natOutgoing: true`（出向 SNAT）** 与 **外部旧内核服务器（Linux `< 4.12`，如 CentOS 7 `3.10.0`）开启了 `net.ipv4.tcp_tw_recycle = 1` + `net.ipv4.tcp_timestamps = 1`** 触发的 **PAWS（Protect Against Wrapped Sequences）时间戳倒退静默丢包**：

```mermaid
flowchart LR
    subgraph Worker["K8s Worker 节点 (Linux 5.14 · Node IP: 192.168.118.202)"]
        Pod["业务 Pod (172.31.226.52)\nTCP TSval = 3,552,269,561"]
        NodeProc["宿主机 Node 进程 (192.168.118.202)\nTCP TSval = 2,953,983,137"]
        SNAT["Calico natOutgoing SNAT\n仅改写源 IP → 192.168.118.202\n不改写 TCP TSval 选项"]
        NIC["物理网卡 ens4"]
    end

    subgraph Legacy["外部旧内核 Web 服务 (CentOS 7.9 / 3.10.0 · 192.168.118.80:18080)"]
        PAWS["tcp_tw_recycle=1 + tcp_timestamps=1\n按源 IP 缓存最近 TSval (TTL 60s):\ntcpm_ts[192.168.118.202] = 3,552,269,561"]
        Web["HTTP Server (:18080)\nConnection: close → 服务端进 TIME_WAIT"]
    end

    Pod -->|"SYN (TSval=3.55B)"| SNAT --> NIC
    NodeProc -->|"SYN (TSval=2.95B)"| NIC
    NIC -->|"同源 IP 192.168.118.202\nPod SYN: TSval=3.55B ≥ 缓存 → 放行 ✓"| PAWS --> Web
    NIC -.->|"同源 IP 192.168.118.202\nNode SYN: TSval=2.95B < 3.55B → 静默丢弃 ✗\n(TcpExtPAWSPassive++)"| PAWS
```

核心触发链路（逐条实测验证）：
1. **现代节点内核为每个 `(源 IP, 目的 IP)` 生成独立的随机 32 位 TCP `TSval` 偏移**（RFC 7323，Linux 4.10+）：因此同节点上 `Pod (172.31.226.52)` 与 `Node (192.168.118.202)` 携带截然不同的 `TSval`（本实验中 Pod 为 `3,552,269,561`，Node 为 `2,953,983,137`，相差近 6 亿）。
2. **Calico `natOutgoing: true` 对出集群流量做 `MASQUERADE`（SNAT）**：Pod 的包离开节点 `ens4` 时源 IP 变成 Node IP `192.168.118.202`，但 **TCP 选项里的 `TSval` 保持不变**。
3. **旧内核外部服务器开启 `net.ipv4.tcp_tw_recycle=1`**：服务端主动关闭连接进入 `TIME_WAIT` 时，按 **源 IP（`192.168.118.202`）** 缓存 `tcpm_ts = 3,552,269,561`（有效期 60 秒）。
4. **宿主机 Node 直接发起连接**：同源 IP `192.168.118.202` 携带着更小的 `TSval = 2,953,983,137` 到达，服务端判定时间戳“倒退”违反 PAWS 规则，**静默丢弃 `SYN` 且不回任何 `RST`**, 内核 `PAWSPassive` 与 `ListenDrops` 计数器同步递增！

#### 实验部署与复现步骤

1. **在外部 CentOS 7（Linux `3.10.0`）主机 `192.168.118.80` 上启动短连接 Web 服务（`:18080`）并开启 `tcp_tw_recycle=1`**：
   ```bash
   # 启动 HTTP/1.0 短连接服务（服务端主动 close 进入 TIME_WAIT，触发 tcp_metrics 更新）
   python -c '
   import json, BaseHTTPServer, SocketServer
   class S(SocketServer.ThreadingMixIn, BaseHTTPServer.HTTPServer):
       daemon_threads = True; allow_reuse_address = True
   class H(BaseHTTPServer.BaseHTTPRequestHandler):
       protocol_version = "HTTP/1.0"
       def do_GET(self):
           b = json.dumps({"server": "centos7-legacy-web", "peer_ip": self.client_address[0], "status": "ok"})
           self.send_response(200); self.send_header("Content-Type", "application/json")
           self.send_header("Content-Length", str(len(b))); self.send_header("Connection", "close")
           self.end_headers(); self.wfile.write(b); self.close_connection = True
       def log_message(self, *a): pass
   S(("0.0.0.0", 18080), H).serve_forever()
   ' &

   # 开启罪魁祸首参数（仅 Linux < 4.12 存在）
   sysctl -w net.ipv4.tcp_tw_recycle=1
   sysctl net.ipv4.tcp_tw_recycle net.ipv4.tcp_timestamps
   # 输出：net.ipv4.tcp_tw_recycle = 1 / net.ipv4.tcp_timestamps = 1
   ```

2. **在 K8s 同一台 Worker 节点上同时部署 Pod 客户端与 `hostNetwork` Node 客户端（[lab/06-tw-recycle-clients.yaml](lab/06-tw-recycle-clients.yaml)）**：
   ```bash
   kubectl apply -f docs/lab/06-tw-recycle-clients.yaml
   ```
   当某 Pod（本实验为 `lab-ext-pod-58c5d4fbdd-tcclt`，`172.31.226.52`）的 32 位 `TSval` 领先于宿主机 Node（`192.168.118.202`）时，立刻观察到惊人的两极分化日志：
   ```text
   # 业务 Pod (SNAT -> 192.168.118.202)：100% 丝滑成功！
   [OK #61] lab-ext-pod-58c5d4fbdd-tcclt (172.31.226.52) -> 200 2.4ms
   [OK #62] lab-ext-pod-58c5d4fbdd-tcclt (172.31.226.52) -> 200 1.7ms

   # 同节点宿主机 Node (192.168.118.202 直接访问)：100% 超时失败！
   [FAIL #468] NODE mw-workergroup1-dlgj9-ztb2d (192.168.118.202) -> 2002.0ms <urlopen error timed out>
   [FAIL #469] NODE mw-workergroup1-dlgj9-ztb2d (192.168.118.202) -> 2002.5ms <urlopen error timed out>
   ```

#### 使用 calico-plumber 组合排障定位全流程

1. **第一步 — 流量拓扑：一屏惊现“绿 Pod vs 红 Node”指向同一外部 IP**：
   在 **calico-plumber「流量拓扑」** 选择 **「全部命名空间」** + **`Pod`** 粒度 + **`最近 1m`**，在顶部过滤框输入 `192.168.118.80`：
   - **上方绿线**：`lab-ext-pod-58c5d4fbdd-tcclt → 192.168.118.80`，吞吐 **`776 B/s`**、平均延迟 **`0.50 ms`**、重传 **`0`**，底部实时流表满屏绿色 `0.23ms`；
   - **下方红线**：同宿主机 `mw-workergroup1-dlgj9-ztb2d (Node 192.168.118.202) → 192.168.118.80` 标红 **`critical`**，展开右侧抽屉显示 **`实时吞吐 0 B/s`、`21` 条五元组持续 `SYN` 重传**！

   ![06-calico-plumber 拓扑同屏对比绿Pod与红Node](lab/screenshots/06-plumber-tw-topology.png)

2. **第二步 — Pod 排障：TCP 三次握手精准对比“秒通 vs 静默超时”**：
   在 **「Pod 排障 → TCP 三次握手检测」** 中分别选中同一节点上的业务 Pod 与 Node（`hostNetwork`）探测 `192.168.118.80:18080`：
   - **业务 Pod (`172.31.226.52`)**：**`3/3 ESTABLISHED`**，`SYN → SYN-ACK` 仅 **`0.31 ms`**；
   - **同节点 Node (`192.168.118.202`)**：**`0/2 SYN_TIMEOUT`**，`SYN` 发出后 3000ms 无任何 `SYN-ACK` 或 `RST` 回包，立刻排除端口未监听，确认是静默丢弃。

   ![06-calico-plumber Pod 握手 0.31ms 成功](lab/screenshots/06-plumber-tw-handshake-pod-ok.png)

   ![06-calico-plumber 同节点 Node 握手 SYN_TIMEOUT](lab/screenshots/06-plumber-tw-handshake-node-fail.png)

3. **第三步 — TraceFlow 到外部 IP：排除集群内 Calico 路由与策略**：
   在 **「TraceFlow」** 中将目标类型切换为 **「IP / 域名」**（`192.168.118.80:18080`）：
   - 对高 `TSval` 的 `lab-ext-pod-tcclt` 执行追踪：**绿色 `REACHABLE`（`0.50 ms`）**，清晰展示正向与回程观测；
   - 对同节点低 `TSval` 的另一 Pod（`lab-backend`，同样 SNAT 成 `192.168.118.202`）执行追踪：返回 **`DROPPED`**（`Source Pod eth0 OBSERVED → Calico Host veth OBSERVED → Kernel Route Lookup PASSED`，正向已发往外部但无回包），明确将问题定界到节点外部或 SNAT 后路径。

   ![06-calico-plumber TraceFlow Pod 可达](lab/screenshots/06-plumber-tw-traceflow-pod-ok.png)

   ![06-calico-plumber TraceFlow 同节点另一 Pod 外网无回包](lab/screenshots/06-plumber-tw-traceflow-node.png)

4. **第四步 — 宿主机主物理网卡 (`ens4`) TCPdump 抓包：抓到同源 IP 区别对待铁证**：
   在 **「Pod 排障 → TCPdump 实时抓包」** 中将 **抓包网卡位置** 切换为 **`宿主机主物理网卡 (ens4)`**，BPF 过滤 `host 192.168.118.80 and tcp port 18080 and tcp[tcpflags] & tcp-syn != 0`：
   - 抓包终端清楚证明：**所有离开 `ens4` 发往 `192.168.118.80:18080` 的报文源 IP 全部都是 `192.168.118.202`**；
   - Frame #1/#3/#6 (`64448`/`10168`/`9371`，Pod SNAT) 均在 **`0.21–0.31ms`** 内收到 `[SYN,ACK]`；
   - 而 **Frame #5 (`192.168.118.202:57676 → 192.168.118.80:18080 [SYN]`，Node 直发)** 发出后却石沉大海！同源 IP、同目的 IP:Port 在 0.3 秒内被对端完全区别对待，直指 TCP Options（`TSval` 时间戳）！

   ![06-calico-plumber 宿主机 ens4 TCPdump 抓包取证](lab/screenshots/06-plumber-tw-tcpdump-ens4.png)

5. **第五步 — 服务端核验 `TS val` 与 `PAWSPassive` 一锤定音 + 修复验证**：
   登录 `192.168.118.80` 核验入向 `SYN` 的 `TS val` 与内核统计：
   ```text
   # 192.168.118.80 侧实测 tcpdump 与 /proc/net/netstat：
   --- T0: PAWSPassive 778 / ListenDrops 778 ---
   # Pod (SNAT 192.168.118.202): TS val = 3552269561 -> 0.05ms 秒回 [S.]
   22:53:26.864630 IP 192.168.118.202.49785 > 192.168.118.80.18080: Flags [S],  ..., TS val 3552269561
   22:53:26.864683 IP 192.168.118.80.18080  > 192.168.118.202.49785: Flags [S.], ..., ecr 3552269561
   # Node (直发 192.168.118.202): TS val = 2953983137 (落后近 6 亿!) -> 连续 2 次 SYN 静默丢弃!
   22:53:27.493113 IP 192.168.118.202.50552 > 192.168.118.80.18080: Flags [S],  seq 2693201866, ..., TS val 2953983137
   22:53:28.525291 IP 192.168.118.202.50552 > 192.168.118.80.18080: Flags [S],  seq 2693201866, ..., TS val 2953984170
   --- T1: PAWSPassive 780 (+2) / ListenDrops 780 (+2) ---
   ```
   **根治方案**：在外部服务端立即关闭 `net.ipv4.tcp_tw_recycle`（或升级至 Linux 4.12+ 内核）：
   ```bash
   sysctl -w net.ipv4.tcp_tw_recycle=0
   ```
   执行后 **1 秒内验证恢复**：
   ```text
   [OK #288] NODE mw-workergroup1-dlgj9-ztb2d (192.168.118.202) -> 200 1.7ms
   [OK #289] NODE mw-workergroup1-dlgj9-ztb2d (192.168.118.202) -> 200 1.7ms
   # 且 PAWSPassive / ListenDrops 稳定停留在 1027 (+0) 不再增长！
   ```

清理实验 6 客户端：
```bash
kubectl delete -f docs/lab/06-tw-recycle-clients.yaml --ignore-not-found
```

---

## 三、排障决策速查表（基于实验总结）

通过上述 6 组实验，日常遇到 Kubernetes / Calico 网络故障时可按以下决策矩阵快速定位：

| 故障现象 | 首选 calico-plumber 模块 | 关键判据 | 典型根因 |
| :--- | :--- | :--- | :--- |
| **接口变慢、无报错** | `流量拓扑` + `HTTP 瀑布流` | 拓扑 RTT 飙升但重传=0；瀑布流中 TCP 建连与 TTFB 同步拉长、DNS 正常 | 跨节点链路延迟、底层网络拥塞、`tc netem`/QoS 限速 |
| **偶发 1s+ 毛刺或超时** | `流量拓扑` + `TCPdump 抓包` | 拓扑平均 RTT 正常但 **Retransmits 飙升标红**；抓包见乱序 DupACK / 同 Seq 重传 | 物理链路丢包、网卡 RingBuffer/软中断丢包、MTU 不一致 |
| **完全超时（3s+ 无响应）** | `TraceFlow` + `TCP 三次握手` | 握手报告 **`SYN_TIMEOUT`**；TraceFlow 精确标红是在源 Pod、底层网络还是目标 `Calico Ingress Policy` 丢弃 | NetworkPolicy 误配、安全组/防火墙拦截、节点网络分区、BGP 路由缺失 |
| **毫秒级 `Connection Refused`** | `全局健康` + `TCP 三次握手` | 握手报告 `<1ms` **`RST_REFUSED`**；全局健康 Service 穿透审计标红 `broken` 与具体 `PodIP:TargetPort` | Service `targetPort` 错配、容器进程未监听目标端口或绑定了 `127.0.0.1` |
| **同节点 Pod 通、Node（或另一 Pod）访外网超时** | `流量拓扑` + `TCP 握手对比` + `宿主机物理口 TCPdump` | 拓扑同框出现绿 Pod vs 红 Node（0 B/s、重传飙升）；`ens4` 同源 SNAT IP 发出的 `SYN` 一部分秒回 `SYN-ACK`、另一部分石沉大海 | 外部旧内核服务端开启 **`net.ipv4.tcp_tw_recycle=1`** + Calico SNAT 引发 **PAWS 时间戳倒退丢包**（`PAWSPassive++`） |

---

## 四、恢复与清理实验环境

恢复 Service `targetPort: 8080`（可验证 `lab-client` 立即恢复 `[OK] 200 ~3ms`）：
```bash
kubectl apply -f docs/lab/00-target-app.yaml
```

如需彻底卸载实验靶场与 Chaos Mesh：
```bash
kubectl delete -f docs/lab/00-target-app.yaml --ignore-not-found
helm uninstall chaos-mesh -n chaos-mesh
kubectl delete ns chaos-mesh --ignore-not-found
```
