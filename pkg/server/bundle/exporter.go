package bundle

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"

	"calico-netdiag/pkg/model"
)

// BundleInput carries all diagnostic snapshots bundled into a single portable .zip archive.
type BundleInput struct {
	GeneratedAt      time.Time
	Overview         model.ClusterNetworkOverview
	KernelAudit      *model.ClusterKernelAuditReport
	RouteAudit       *model.ClusterRouteAuditReport
	IPAM             model.IPAMSnapshot
	Health           *model.GlobalHealthReport
	TraceFlowHistory []model.TraceFlowResult
	FocusTrace       *model.TraceFlowResult
	FocusPod         *model.PodNetworkDetail
	Incidents        []model.IncidentSnapshot
}

// BuildDiagnosticBundleZip creates an in-memory zip archive containing a self-contained offline HTML report,
// README manifest, and structured JSON artifacts for SRE/network incident sharing.
func BuildDiagnosticBundleZip(in BundleInput) ([]byte, string, error) {
	if in.GeneratedAt.IsZero() {
		in.GeneratedAt = time.Now()
	}

	filename := fmt.Sprintf("calico-plumber-diag-%s.zip", in.GeneratedAt.Format("20060102-150405"))
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// 1. Self-contained offline HTML report
	htmlBytes, err := renderOfflineHTMLReport(in)
	if err != nil {
		_ = zw.Close()
		return nil, "", fmt.Errorf("render offline html report: %w", err)
	}
	if err := addZipBytes(zw, "report.html", htmlBytes); err != nil {
		_ = zw.Close()
		return nil, "", err
	}

	// 2. README manifest
	readme := buildReadme(in, filename)
	if err := addZipBytes(zw, "README.txt", []byte(readme)); err != nil {
		_ = zw.Close()
		return nil, "", err
	}

	// 3. Structured JSON artifacts
	if err := addZipJSON(zw, "01-cluster-overview.json", in.Overview); err != nil {
		_ = zw.Close()
		return nil, "", err
	}
	if in.KernelAudit != nil {
		if err := addZipJSON(zw, "02-kernel-audit.json", in.KernelAudit); err != nil {
			_ = zw.Close()
			return nil, "", err
		}
	}
	if in.RouteAudit != nil {
		if err := addZipJSON(zw, "03-route-bgp-audit.json", in.RouteAudit); err != nil {
			_ = zw.Close()
			return nil, "", err
		}
	}
	if err := addZipJSON(zw, "04-ipam-snapshot.json", in.IPAM); err != nil {
		_ = zw.Close()
		return nil, "", err
	}
	if in.Health != nil {
		if err := addZipJSON(zw, "05-health-report.json", in.Health); err != nil {
			_ = zw.Close()
			return nil, "", err
		}
	}
	if err := addZipJSON(zw, "06-traceflow-history.json", in.TraceFlowHistory); err != nil {
		_ = zw.Close()
		return nil, "", err
	}
	if in.FocusTrace != nil {
		if err := addZipJSON(zw, "06b-traceflow-focus.json", in.FocusTrace); err != nil {
			_ = zw.Close()
			return nil, "", err
		}
	}
	if in.FocusPod != nil {
		if err := addZipJSON(zw, "07-pod-detail.json", in.FocusPod); err != nil {
			_ = zw.Close()
			return nil, "", err
		}
	}
	if len(in.Incidents) > 0 {
		if err := addZipJSON(zw, "08-incidents.json", in.Incidents); err != nil {
			_ = zw.Close()
			return nil, "", err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, "", fmt.Errorf("finalize zip: %w", err)
	}
	return buf.Bytes(), filename, nil
}

func addZipJSON(zw *zip.Writer, name string, v interface{}) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	return addZipBytes(zw, name, b)
}

func addZipBytes(zw *zip.Writer, name string, data []byte) error {
	hdr := &zip.FileHeader{
		Name:     name,
		Method:   zip.Deflate,
		Modified: time.Now(),
	}
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", name, err)
	}
	_, err = w.Write(data)
	return err
}

func buildReadme(in BundleInput, filename string) string {
	var b strings.Builder
	b.WriteString("================================================================\n")
	b.WriteString("  calico-plumber 故障现场诊断包 (Diagnostic Bundle)\n")
	b.WriteString("================================================================\n\n")
	b.WriteString(fmt.Sprintf("Bundle File : %s\n", filename))
	b.WriteString(fmt.Sprintf("Generated At: %s\n", in.GeneratedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("Kubernetes  : %s\n", in.Overview.K8sVersion))
	b.WriteString(fmt.Sprintf("Calico      : %s (%s / %s)\n", in.Overview.CalicoVersion, in.Overview.PrimaryEncapMode, in.Overview.LinuxDataplane))
	b.WriteString(fmt.Sprintf("Cluster Size: %d Nodes, %d Pods, %d Services\n\n", len(in.Overview.Nodes), in.Overview.PodCount, in.Overview.ServiceCount))
	b.WriteString("文件清单:\n")
	b.WriteString("  - report.html               : 独立离线诊断报告（直接用浏览器打开或打印 PDF）\n")
	b.WriteString("  - 01-cluster-overview.json  : 集群架构、节点状态、BGP Peer、IPPool 与全局流量摘要\n")
	b.WriteString("  - 02-kernel-audit.json      : 节点内核网络 / Conntrack / SNAT / Sysctl / MTU 隐患体检\n")
	b.WriteString("  - 03-route-bgp-audit.json   : Expected vs Actual 路由一致性审计、非对称路由与 BGP 抖动时间线\n")
	b.WriteString("  - 04-ipam-snapshot.json     : Calico IPPool 与 IPAMBlock 逐 IP 分配详情\n")
	b.WriteString("  - 05-health-report.json     : 全局连通性巡检矩阵（Pod-Pod / Pod-Node / CoreDNS / Service）\n")
	b.WriteString("  - 06-traceflow-history.json : 近期多跳 TraceFlow 路径追踪历史记录\n")
	if in.FocusTrace != nil {
		b.WriteString(fmt.Sprintf("  - 06b-traceflow-focus.json  : 本次聚焦 TraceFlow (%s)\n", in.FocusTrace.ID))
	}
	if in.FocusPod != nil {
		b.WriteString(fmt.Sprintf("  - 07-pod-detail.json        : 指定 Pod Netns 与策略详情 (%s/%s)\n", in.FocusPod.Pod.Namespace, in.FocusPod.Pod.Name))
	}
	return b.String()
}

func renderOfflineHTMLReport(in BundleInput) ([]byte, error) {
	funcMap := template.FuncMap{
		"fmtTime": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.Format("2006-01-02 15:04:05 MST")
		},
		"fmtFloat": func(f float64) string {
			return fmt.Sprintf("%.2f", f)
		},
		"badgeClass": func(s string) string {
			switch strings.ToLower(s) {
			case "healthy", "pass", "passed", "reachable", "delivered", "observed", "established", "up":
				return "badge-ok"
			case "warning", "warn", "degraded", "nat", "encapsulated", "decapsulated":
				return "badge-warn"
			case "critical", "fail", "failed", "dropped", "timeout", "error", "unreachable", "broken":
				return "badge-err"
			default:
				return "badge-info"
			}
		},
	}

	tmpl, err := template.New("report").Funcs(funcMap).Parse(offlineReportHTMLTemplate)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, in); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

const offlineReportHTMLTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>calico-plumber 故障现场诊断报告</title>
<style>
  :root {
    --bg: #fbf9f6; --card: #ffffff; --border: #e9e1d6;
    --text: #201b18; --muted: #8c7f73; --primary: #d9720b;
    --ok-bg: #ecfdf5; --ok-fg: #047857; --ok-bd: #a7f3d0;
    --warn-bg: #fff4e4; --warn-fg: #9e4a05; --warn-bd: #fad4a1;
    --err-bg: #fef2f2; --err-fg: #b91c1c; --err-bd: #fecaca;
    --info-bg: #edf5fc; --info-fg: #155a96; --info-bd: #c4dcf0;
  }
  * { box-sizing: border-box; }
  body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "PingFang SC", "Inter", "Segoe UI", sans-serif; background: var(--bg); color: var(--text); line-height: 1.5; font-size: 13px; }
  .wrap { max-width: 1180px; margin: 0 auto; padding: 24px 20px 48px; }
  .header { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: flex-start; gap: 16px; padding: 20px 24px; background: var(--card); border: 1px solid var(--border); border-radius: 14px; margin-bottom: 18px; }
  .title { font-size: 20px; font-weight: 700; margin: 0; color: var(--text); }
  .subtitle { font-size: 12px; color: var(--muted); margin-top: 4px; }
  .meta-pills { display: flex; flex-wrap: wrap; gap: 8px; }
  .pill { padding: 4px 10px; border-radius: 999px; font-size: 11.5px; font-weight: 600; background: #f1f5f9; color: #334155; border: 1px solid var(--border); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .kpi-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(195px, 1fr)); gap: 12px; margin-bottom: 18px; }
  .kpi { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 14px 16px; }
  .kpi-label { font-size: 11px; text-transform: uppercase; letter-spacing: 0.04em; color: var(--muted); font-weight: 600; }
  .kpi-val { font-size: 22px; font-weight: 700; margin-top: 4px; color: var(--text); }
  .kpi-sub { font-size: 11.5px; color: var(--muted); margin-top: 2px; }
  .section { background: var(--card); border: 1px solid var(--border); border-radius: 14px; padding: 18px 20px; margin-bottom: 16px; }
  .section h2 { margin: 0 0 12px; font-size: 15px; font-weight: 700; display: flex; align-items: center; justify-content: space-between; border-bottom: 1px solid #f1f5f9; padding-bottom: 8px; }
  .section h3 { margin: 14px 0 8px; font-size: 13px; font-weight: 700; color: #334155; }
  table { width: 100%; border-collapse: collapse; font-size: 12px; margin-top: 6px; }
  th, td { text-align: left; padding: 7px 10px; border-bottom: 1px solid #f1f5f9; vertical-align: top; }
  th { font-size: 11px; text-transform: uppercase; letter-spacing: 0.03em; color: var(--muted); background: #f8fafc; font-weight: 600; }
  .mono { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 11.5px; }
  .badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: 600; border: 1px solid transparent; white-space: nowrap; }
  .badge-ok { background: var(--ok-bg); color: var(--ok-fg); border-color: var(--ok-bd); }
  .badge-warn { background: var(--warn-bg); color: var(--warn-fg); border-color: var(--warn-bd); }
  .badge-err { background: var(--err-bg); color: var(--err-fg); border-color: var(--err-bd); }
  .badge-info { background: var(--info-bg); color: var(--info-fg); border-color: var(--info-bd); }
  .finding { border: 1px solid var(--border); border-radius: 10px; padding: 10px 12px; margin-bottom: 8px; background: #fcfcfd; }
  .finding-title { font-weight: 700; margin-bottom: 4px; display: flex; align-items: center; gap: 8px; }
  .finding-rem { margin-top: 6px; padding: 6px 8px; background: #f1f5f9; border-radius: 6px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 11px; color: #1e293b; overflow-x: auto; }
  .empty { color: var(--muted); font-size: 12px; padding: 8px 0; }
  @media print { body { background: #fff; } .section { break-inside: avoid; } }
</style>
</head>
<body>
<div class="wrap">
  <div class="header">
    <div>
      <h1 class="title">calico-plumber 故障现场诊断报告</h1>
      <div class="subtitle">离线诊断快照 · 生成时间 {{fmtTime .GeneratedAt}} · 附带完整 JSON 原始证据</div>
    </div>
    <div class="meta-pills">
      <span class="pill">K8s {{.Overview.K8sVersion}}</span>
      <span class="pill">Calico {{.Overview.CalicoVersion}}</span>
      <span class="pill">Encap: {{.Overview.PrimaryEncapMode}}</span>
      <span class="pill">Dataplane: {{.Overview.LinuxDataplane}}</span>
      <span class="pill">kube-proxy: {{.Overview.KubeProxyMode}}</span>
    </div>
  </div>

  <div class="kpi-grid">
    <div class="kpi">
      <div class="kpi-label">集群规模</div>
      <div class="kpi-val">{{len .Overview.Nodes}} Nodes</div>
      <div class="kpi-sub">{{.Overview.PodCount}} Pods · {{.Overview.ServiceCount}} Services · {{.Overview.NetworkPolicyCnt}} Policies</div>
    </div>
    {{if .KernelAudit}}
    <div class="kpi">
      <div class="kpi-label">内核 / Conntrack 体检</div>
      <div class="kpi-val">{{.KernelAudit.AverageScore}} / 100</div>
      <div class="kpi-sub">严重 {{.KernelAudit.CriticalCount}} · 警告 {{.KernelAudit.WarningCount}} · <span class="badge {{badgeClass .KernelAudit.OverallStatus}}">{{.KernelAudit.OverallStatus}}</span></div>
    </div>
    {{end}}
    {{if .RouteAudit}}
    <div class="kpi">
      <div class="kpi-label">路由与 BGP 一致性</div>
      <div class="kpi-val">{{.RouteAudit.TotalDrifts}} 项偏差</div>
      <div class="kpi-sub">缺失 {{.RouteAudit.MissingCount}} · 幽灵路由 {{.RouteAudit.GhostPodCount}} · 非对称 {{.RouteAudit.AsymmetricCount}}</div>
    </div>
    {{end}}
    {{if .Health}}
    <div class="kpi">
      <div class="kpi-label">全局连通性巡检</div>
      <div class="kpi-val">{{.Health.OverallScore}} / 100</div>
      <div class="kpi-sub">Pod Mesh {{.Health.PodToPod.HealthyPairs}}/{{.Health.PodToPod.TotalPairs}} · Service {{.Health.PodToService.HealthyChecks}}/{{.Health.PodToService.TotalChecks}}</div>
    </div>
    {{end}}
  </div>

  {{if .FocusTrace}}
  <div class="section">
    <h2>聚焦 TraceFlow 路径追踪 <span class="mono" style="font-size:11px;color:#64748b;margin-left:8px;">({{.FocusTrace.ID}})</span> <span class="badge {{badgeClass .FocusTrace.Status}}">{{.FocusTrace.Status}} · {{fmtFloat .FocusTrace.TotalLatencyMs}} ms</span></h2>
    <div style="margin-bottom:10px;">
      <strong>{{.FocusTrace.Source.DisplayName}}</strong> <span class="mono">({{.FocusTrace.Source.IP}})</span>
      → <strong>{{.FocusTrace.Destination.DisplayName}}</strong> <span class="mono">({{.FocusTrace.Destination.IP}})</span>
      · 协议 <span class="mono">{{.FocusTrace.Protocol}}:{{.FocusTrace.DstPort}}</span> · 封装 <span class="mono">{{.FocusTrace.EncapMode}}</span>
    </div>
    <div style="margin-bottom:10px;color:#334155;">{{.FocusTrace.VerdictSummary}}</div>
    {{if .FocusTrace.RootCauseHint}}<div class="finding-rem">根因建议: {{.FocusTrace.RootCauseHint}}</div>{{end}}
    <table>
      <thead><tr><th>#</th><th>阶段</th><th>节点</th><th>组件 / 接口</th><th>状态</th><th>实测抓包摘要</th></tr></thead>
      <tbody>
        {{range .FocusTrace.ForwardHops}}
        <tr>
          <td class="mono">#{{.Index}}</td>
          <td class="mono">{{.Lane}}</td>
          <td class="mono">{{.NodeName}}</td>
          <td><strong>{{.Title}}</strong><div class="mono" style="color:#64748b">{{.Interface}} {{.Subtitle}}</div></td>
          <td><span class="badge {{badgeClass .Status}}">{{.Status}}</span></td>
          <td class="mono">{{.PacketSummary}}{{if .OuterHeader}} [{{.OuterHeader}}]{{end}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </div>
  {{end}}

  {{if .KernelAudit}}
  <div class="section">
    <h2>网络平面体检：模式 · MTU · MAC/ARP · NAT 与底层内核证据 <span class="badge {{badgeClass .KernelAudit.OverallStatus}}">{{.KernelAudit.OverallStatus}} ({{.KernelAudit.AverageScore}}/100)</span></h2>
    {{if .KernelAudit.NetworkMode.Summary}}
    <div style="margin-bottom:10px;padding:10px 12px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:10px;font-size:12px;">
      <strong>Calico 网络模式 ({{.KernelAudit.NetworkMode.EncapMode}})</strong>：{{.KernelAudit.NetworkMode.Summary}}
      <div class="mono" style="margin-top:4px;color:#475569;">物理 MTU={{.KernelAudit.NetworkMode.PhysicalMTU}}B · 封装开销={{.KernelAudit.NetworkMode.EncapOverheadBytes}}B · 推荐 Pod MTU={{.KernelAudit.NetworkMode.RecommendedPodMTU}}B · NAT Outgoing Pools={{.KernelAudit.NetworkMode.NATOutgoingPools}}/{{.KernelAudit.NetworkMode.TotalPools}}</div>
    </div>
    {{end}}
    {{if .KernelAudit.Findings}}
      {{range .KernelAudit.Findings}}
      <div class="finding">
        <div class="finding-title">
          <span class="badge {{badgeClass .Severity}}">{{.Severity}}</span>
          {{if .NodeName}}<span class="pill">{{.NodeName}}</span>{{end}}
          <span>{{.Title}}</span>
        </div>
        <div>{{.Detail}}</div>
        {{if .KernelEvidence}}<div class="mono" style="margin-top:4px;color:#64748b;">底层内核证据: {{.KernelEvidence}}</div>{{end}}
        {{if .Remediation}}<div class="finding-rem">修复建议: {{.Remediation}}</div>{{end}}
      </div>
      {{end}}
    {{else}}
      <div class="empty">所有节点的二层 MAC/ARP、多层 MTU、NAT 转发与底层内核状态均健康，无隐患项。</div>
    {{end}}

    <h3>各节点二层 MAC / 网关 ARP / MTU 与 NAT 状态</h3>
    <table>
      <thead>
        <tr>
          <th>节点</th><th>主网卡 / MAC</th><th>默认网关 ARP</th><th>Proxy ARP / ARP 表</th><th>物理 NIC / Tunnel / Veth MTU</th><th>SNAT &amp; Conntrack 水位</th>
        </tr>
      </thead>
      <tbody>
        {{range .KernelAudit.Nodes}}
        <tr>
          <td><strong>{{.NodeName}}</strong><div class="mono" style="color:#64748b">{{.InternalIP}}</div></td>
          <td class="mono">{{.L2.PrimaryNIC}}<div style="color:#64748b">{{.L2.PrimaryMAC}}</div></td>
          <td class="mono">{{.L2.DefaultGateway}} {{if .L2.GatewayMAC}}({{.L2.GatewayMAC}}){{end}} {{if .L2.GatewayARPState}}<span class="badge {{badgeClass .L2.GatewayARPState}}">{{.L2.GatewayARPState}}</span>{{end}}</td>
          <td class="mono">ProxyARP {{.L2.ProxyARPVeths}}/{{.L2.TotalCaliVeths}} · ARP {{.L2.ARPCount}}/{{.L2.ARPGCThresh3}}</td>
          <td class="mono">{{.MTU.PrimaryNIC}}={{.MTU.PrimaryMTU}}{{if .MTU.TunnelIface}} · {{.MTU.TunnelIface}}={{.MTU.TunnelMTU}}{{end}} · veth({{.MTU.SampledVethCount}})={{.MTU.MinVethMTU}}..{{.MTU.MaxVethMTU}}</td>
          <td class="mono">{{if .Conntrack.SNATRandomFully}}random-fully ✓{{else}}random-fully ✗{{end}} · CT {{fmtFloat .Conntrack.UsagePct}}% (drop={{.Conntrack.Drop}})</td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </div>
  {{end}}

  {{if .RouteAudit}}
  <div class="section">
    <h2>路由与 BGP 一致性审计 (Expected vs Actual Route Diff) <span class="badge {{badgeClass .RouteAudit.OverallStatus}}">{{.RouteAudit.OverallStatus}}</span></h2>
    {{if .RouteAudit.Drifts}}
      {{range .RouteAudit.Drifts}}
      <div class="finding">
        <div class="finding-title">
          <span class="badge {{badgeClass .Severity}}">{{.Severity}}</span>
          <span class="pill">{{.Kind}}</span>
          <span class="pill">{{.NodeName}}</span>
          <span class="mono">{{.CIDR}}</span>
        </div>
        <div>{{.Detail}}</div>
        <div class="mono" style="margin-top:4px;color:#475569;">Expected: {{.Expected}} | Actual: {{.Actual}}</div>
        {{if .Remediation}}<div class="finding-rem">排查建议: {{.Remediation}}</div>{{end}}
      </div>
      {{end}}
    {{else}}
      <div class="empty">集群所有节点的 IPAMBlock 路由、Blackhole 防环路路由、本地 Pod /32 路由与跨节点路径对称性完全一致，零偏差。</div>
    {{end}}

    <h3>各节点路由表与 BGP 汇总</h3>
    <table>
      <thead><tr><th>节点</th><th>状态</th><th>总路由</th><th>BIRD 路由</th><th>本地 Pod /32</th><th>Blackhole</th><th>BGP Peers</th><th>偏差数</th></tr></thead>
      <tbody>
        {{range .RouteAudit.Nodes}}
        <tr>
          <td><strong>{{.NodeName}}</strong> <span class="mono" style="color:#64748b">({{.InternalIP}})</span></td>
          <td><span class="badge {{badgeClass .Status}}">{{.Status}}</span></td>
          <td class="mono">{{.TotalRoutes}}</td>
          <td class="mono">{{.BIRDRoutes}}</td>
          <td class="mono">{{.LocalPodRoutes}} / {{.ExpectedLocalPod}}</td>
          <td class="mono">{{.BlackholeRoutes}}</td>
          <td class="mono">{{.BGPPeersUp}} / {{.BGPPeersTotal}}</td>
          <td class="mono">{{.DriftCount}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>

    {{if .RouteAudit.BGPEvents}}
    <h3>BGP 会话状态变更 / 抖动时间线</h3>
    <table>
      <thead><tr><th>时间</th><th>节点</th><th>Peer</th><th>状态变化</th><th>级别</th><th>说明</th></tr></thead>
      <tbody>
        {{range .RouteAudit.BGPEvents}}
        <tr>
          <td class="mono">{{fmtTime .Timestamp}}</td>
          <td class="mono">{{.NodeName}}</td>
          <td class="mono">{{.PeerName}} ({{.PeerIP}})</td>
          <td class="mono">{{.PrevState}} → {{.NewState}}</td>
          <td><span class="badge {{badgeClass .Severity}}">{{.Severity}}</span></td>
          <td>{{.Message}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{end}}
  </div>
  {{end}}

  <div class="section">
    <h2>集群节点与 Calico IPPool 概览</h2>
    <table>
      <thead><tr><th>节点</th><th>Internal IP</th><th>PodCIDR</th><th>Pods / Blocks</th><th>遥测引擎</th><th>主网卡 / MTU</th></tr></thead>
      <tbody>
        {{range .Overview.Nodes}}
        <tr>
          <td><strong>{{.Name}}</strong></td>
          <td class="mono">{{.InternalIP}}</td>
          <td class="mono">{{.PodCIDR}}</td>
          <td class="mono">{{.PodCount}} / {{.BlockCount}} ({{.AllocatedIPs}} IPs)</td>
          <td><span class="badge badge-info">{{.TelemetryEngine}}</span></td>
          <td class="mono">{{.PrimaryNIC}} (MTU {{.NICMTU}})</td>
        </tr>
        {{end}}
      </tbody>
    </table>

    {{if .IPAM.Pools}}
    <h3>IPPool 利用率</h3>
    <table>
      <thead><tr><th>Pool 名称</th><th>CIDR</th><th>封装模式</th><th>NAT Outgoing</th><th>已分配 / 总容量</th><th>借用 / 孤儿 IP</th><th>利用率</th></tr></thead>
      <tbody>
        {{range .IPAM.Pools}}
        <tr>
          <td class="mono"><strong>{{.Name}}</strong></td>
          <td class="mono">{{.CIDR}}</td>
          <td><span class="badge badge-info">{{.EncapMode}}</span></td>
          <td class="mono">{{.NATOutgoing}}</td>
          <td class="mono">{{.AllocatedIPs}} / {{.TotalIPs}}</td>
          <td class="mono">{{.BorrowedIPs}} / {{.OrphanIPs}}</td>
          <td class="mono">{{fmtFloat .UtilizationPercent}}%</td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{end}}
  </div>

  {{if .FocusPod}}
  <div class="section">
    <h2>指定 Pod 网络命名空间详情: {{.FocusPod.Pod.Namespace}}/{{.FocusPod.Pod.Name}}</h2>
    <div class="mono" style="margin-bottom:8px;">
      PodIP={{.FocusPod.Pod.IP}} · Node={{.FocusPod.Pod.NodeName}} · HostVeth={{.FocusPod.HostVeth}} · PodIface={{.FocusPod.PodIface}} · MAC={{.FocusPod.PodMAC}} · MTU={{.FocusPod.PodMTU}}
    </div>
    <div class="mono" style="color:#475569;">Host Route: {{.FocusPod.HostRouteToPod}}</div>
  </div>
  {{end}}

  {{if .TraceFlowHistory}}
  <div class="section">
    <h2>近期 TraceFlow 追踪记录 (共 {{len .TraceFlowHistory}} 条)</h2>
    <table>
      <thead><tr><th>Trace ID</th><th>时间</th><th>源 → 目标</th><th>协议</th><th>封装</th><th>耗时</th><th>结果</th></tr></thead>
      <tbody>
        {{range .TraceFlowHistory}}
        <tr>
          <td class="mono">{{.ID}}</td>
          <td class="mono">{{fmtTime .StartedAt}}</td>
          <td class="mono">{{.Source.DisplayName}} ({{.Source.IP}}) → {{.Destination.DisplayName}} ({{.Destination.IP}})</td>
          <td class="mono">{{.Protocol}}:{{.DstPort}}</td>
          <td class="mono">{{.EncapMode}}</td>
          <td class="mono">{{fmtFloat .TotalLatencyMs}} ms</td>
          <td><span class="badge {{badgeClass .Status}}">{{.Status}}</span></td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </div>
  {{end}}
</div>
</body>
</html>`
