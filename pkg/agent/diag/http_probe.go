package diag

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"time"

	"calico-netdiag/pkg/agent/netns"
	"calico-netdiag/pkg/model"
)

// RunHTTPProbe executes an HTTP/HTTPS request strictly inside the source Pod's netns,
// returning Chrome/curl-style waterfall latency metrics, TLS info, headers, and body preview.
func RunHTTPProbe(ctx context.Context, podUID, podIP, podDisplay string, req model.HTTPProbeRequest) (*model.HTTPProbeResponse, error) {
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	req.Method = strings.ToUpper(req.Method)
	if req.TimeoutMs <= 0 || req.TimeoutMs > 30000 {
		req.TimeoutMs = 5000
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		req.URL = "http://" + req.URL
	}

	info, err := netns.LocatePod(podUID, podIP)
	if err != nil {
		return nil, fmt.Errorf("locate source pod: %w", err)
	}

	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	resp := &model.HTTPProbeResponse{
		SrcPod: podDisplay,
		SrcIP:  podIP,
		URL:    req.URL,
		Method: req.Method,
	}

	var (
		dnsStart, dnsDone         time.Time
		connStart, connDone       time.Time
		tlsStart, tlsDone         time.Time
		wroteReq, firstRespByte   time.Time
		resolvedIP, remoteAddr    string
		tlsState                  *tls.ConnectionState
	)

	trace := &httptrace.ClientTrace{
		DNSStart: func(_ httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone: func(di httptrace.DNSDoneInfo) {
			dnsDone = time.Now()
			if len(di.Addrs) > 0 {
				resolvedIP = di.Addrs[0].IP.String()
			}
		},
		ConnectStart: func(_, _ string) { connStart = time.Now() },
		ConnectDone: func(_, addr string, _ error) {
			connDone = time.Now()
			remoteAddr = addr
		},
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone: func(cs tls.ConnectionState, _ error) {
			tlsDone = time.Now()
			st := cs
			tlsState = &st
		},
		WroteRequest:         func(_ httptrace.WroteRequestInfo) { wroteReq = time.Now() },
		GotFirstResponseByte: func() { firstRespByte = time.Now() },
	}

	// Custom resolver + dialer locked strictly inside the Pod's network namespace
	podResolver := netns.NewNetnsResolver(info.NetnsPath, timeout)
	transport := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: req.InsecureSkipTLS},
		DialContext: func(dctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			dialIP := host
			if net.ParseIP(host) == nil {
				dnsStart = time.Now()
				ips, rerr := podResolver.LookupIP(dctx, "ip4", host)
				dnsDone = time.Now()
				if rerr != nil || len(ips) == 0 {
					return nil, fmt.Errorf("dns lookup %s: %w", host, rerr)
				}
				dialIP = ips[0].String()
				resolvedIP = dialIP
			}
			target := net.JoinHostPort(dialIP, port)
			connStart = time.Now()
			c, cerr := netns.DialInNetns(dctx, info.NetnsPath, "tcp4", target, nil, timeout)
			connDone = time.Now()
			if cerr == nil {
				remoteAddr = target
			}
			return c, cerr
		},
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	if !req.FollowRedirects {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	var bodyReader io.Reader
	if req.Body != "" {
		bodyReader = strings.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), req.Method, req.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build http request: %w", err)
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	if req.HostHeader != "" {
		httpReq.Host = req.HostHeader
	}
	if httpReq.Header.Get("User-Agent") == "" {
		httpReq.Header.Set("User-Agent", "calico-plumber/0.1")
	}

	overallStart := time.Now()
	httpResp, err := client.Do(httpReq)
	if !dnsStart.IsZero() && !dnsDone.IsZero() {
		resp.DNSLookupMs = roundMs(dnsDone.Sub(dnsStart))
	}
	if !connStart.IsZero() && !connDone.IsZero() {
		resp.TCPConnectMs = roundMs(connDone.Sub(connStart))
	}
	if !tlsStart.IsZero() && !tlsDone.IsZero() {
		resp.TLSHandshakeMs = roundMs(tlsDone.Sub(tlsStart))
	}
	resp.ResolvedIP = resolvedIP
	resp.RemoteAddr = remoteAddr

	if err != nil {
		resp.TotalMs = roundMs(time.Since(overallStart))
		resp.Error = err.Error()
		resp.Diagnosis = classifyHTTPError(err)
		return resp, nil
	}
	defer httpResp.Body.Close()

	bodyBytes, _ := io.ReadAll(io.LimitReader(httpResp.Body, 64*1024))
	doneTime := time.Now()

	if !wroteReq.IsZero() && !firstRespByte.IsZero() {
		resp.ServerWaitTTFBMs = roundMs(firstRespByte.Sub(wroteReq))
	}
	if !firstRespByte.IsZero() {
		resp.ContentXferMs = roundMs(doneTime.Sub(firstRespByte))
	}
	resp.TotalMs = roundMs(doneTime.Sub(overallStart))
	resp.Success = httpResp.StatusCode >= 200 && httpResp.StatusCode < 400
	resp.StatusCode = httpResp.StatusCode
	resp.StatusText = httpResp.Status
	resp.Protocol = httpResp.Proto
	resp.ResponseHeaders = httpResp.Header
	resp.BodyBytes = len(bodyBytes)
	if len(bodyBytes) > 4096 {
		resp.BodyPreview = string(bodyBytes[:4096]) + "\n... (truncated)"
	} else {
		resp.BodyPreview = string(bodyBytes)
	}

	if tlsState != nil {
		resp.TLSVersion = tlsVersionName(tlsState.Version)
		resp.TLSCipher = tls.CipherSuiteName(tlsState.CipherSuite)
		if len(tlsState.PeerCertificates) > 0 {
			cert := tlsState.PeerCertificates[0]
			resp.TLSCertSubject = cert.Subject.CommonName
			resp.TLSCertExpiry = cert.NotAfter.Format("2006-01-02")
		}
	}

	switch {
	case httpResp.StatusCode >= 200 && httpResp.StatusCode < 400:
		resp.Diagnosis = fmt.Sprintf("HTTP %d 正常响应，总耗时 %.2f ms (DNS %.2f + TCP %.2f + TLS %.2f + TTFB %.2f ms)",
			httpResp.StatusCode, resp.TotalMs, resp.DNSLookupMs, resp.TCPConnectMs, resp.TLSHandshakeMs, resp.ServerWaitTTFBMs)
	case httpResp.StatusCode >= 500:
		resp.Diagnosis = fmt.Sprintf("网络层可达，但上游服务端返回 %d 错误（应用/网关故障），TTFB %.2f ms", httpResp.StatusCode, resp.ServerWaitTTFBMs)
	default:
		resp.Diagnosis = fmt.Sprintf("网络层可达，服务端返回 HTTP %d（总耗时 %.2f ms）", httpResp.StatusCode, resp.TotalMs)
	}

	return resp, nil
}

func classifyHTTPError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "dns lookup") || strings.Contains(msg, "no such host"):
		return "DNS 解析失败：请检查服务域名拼写与 CoreDNS 可达性"
	case strings.Contains(msg, "connection refused"):
		return "TCP 连接被拒 (RST)：网络路径可达，但目标端口未启动服务"
	case strings.Contains(msg, "x509:") || strings.Contains(msg, "tls:"):
		return "TLS 握手失败：证书校验不通过或协议版本不匹配（可勾选忽略证书校验测试）"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return "请求超时：可能存在 NetworkPolicy 阻断、跨节点隧道异常或服务端无响应"
	default:
		return "HTTP 探测失败: " + msg
	}
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS10:
		return "TLS 1.0"
	default:
		return fmt.Sprintf("0x%x", v)
	}
}
