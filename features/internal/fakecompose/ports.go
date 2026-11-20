package fakecompose

import (
	"fmt"
	"strconv"
	"strings"
)

// normalizePort 把 ports 的一個項目轉成 compose 的 long form：
// {mode, host_ip（有指定時）, target, published（字串）, protocol}。
//
// 接受 short syntax（"8080"、"8080:8080"、"127.0.0.1:8080:8080"、"[::1]:8080:8080"、"8080:8080/udp"，也接受整數）
// 與 long syntax（map）。port 範圍不支援。
func normalizePort(p any) (map[string]any, error) {
	switch v := p.(type) {
	case int:
		return longPort("", "", v, "tcp")
	case string:
		return parseShortPort(v)
	case map[string]any:
		return normalizeLongPort(v)
	default:
		return nil, &ExitError{Code: 1, Msg: fmt.Sprintf("ports 的項目必須是字串、整數或 mapping，收到 %T", p)}
	}
}

// parseShortPort 解析 `[host_ip:][published:]target[/protocol]`。
func parseShortPort(s string) (map[string]any, error) {
	spec, proto, _ := strings.Cut(s, "/")
	if proto == "" {
		proto = "tcp"
	}
	if proto != "tcp" && proto != "udp" && proto != "sctp" {
		return nil, &ExitError{Code: 1, Msg: fmt.Sprintf("invalid protocol %q in port %q", proto, s)}
	}

	// IPv6 位址以中括號包住，裡面的冒號不是分隔符號。
	hostIP := ""
	if strings.HasPrefix(spec, "[") {
		end := strings.Index(spec, "]")
		if end < 0 || end+1 >= len(spec) || spec[end+1] != ':' {
			return nil, &ExitError{Code: 1, Msg: fmt.Sprintf("invalid port %q", s)}
		}
		hostIP, spec = spec[1:end], spec[end+2:]
	}
	parts := strings.Split(spec, ":")
	var published, target string
	switch {
	case len(parts) == 1:
		target = parts[0]
	case len(parts) == 2:
		published, target = parts[0], parts[1]
	case len(parts) == 3 && hostIP == "":
		hostIP, published, target = parts[0], parts[1], parts[2]
	default:
		return nil, &ExitError{Code: 1, Msg: fmt.Sprintf("invalid port %q", s)}
	}
	if strings.Contains(published, "-") || strings.Contains(target, "-") {
		return nil, unsupported("port 範圍 %q", s)
	}
	t, err := strconv.Atoi(target)
	if err != nil || t < 1 || t > 65535 {
		return nil, &ExitError{Code: 1, Msg: fmt.Sprintf("invalid target port in %q", s)}
	}
	if published != "" {
		if n, err := strconv.Atoi(published); err != nil || n < 0 || n > 65535 {
			return nil, &ExitError{Code: 1, Msg: fmt.Sprintf("invalid published port in %q", s)}
		}
	}
	return longPort(hostIP, published, t, proto)
}

// normalizeLongPort 補上 long syntax 省略的預設值（mode: ingress、protocol: tcp），published 一律轉成字串。
func normalizeLongPort(m map[string]any) (map[string]any, error) {
	target, ok := toInt(m["target"])
	if !ok {
		return nil, &ExitError{Code: 1, Msg: "ports 的 target 必須是整數"}
	}
	published := ""
	if pv, has := m["published"]; has && pv != nil {
		published = fmt.Sprint(pv)
	}
	hostIP, _ := m["host_ip"].(string)
	proto, _ := m["protocol"].(string)
	if proto == "" {
		proto = "tcp"
	}
	out, err := longPort(hostIP, published, target, proto)
	if err != nil {
		return nil, err
	}
	if mode, _ := m["mode"].(string); mode != "" {
		out["mode"] = mode
	}
	return out, nil
}

func longPort(hostIP, published string, target int, proto string) (map[string]any, error) {
	out := map[string]any{"mode": "ingress", "target": target, "protocol": proto}
	if hostIP != "" {
		out["host_ip"] = hostIP
	}
	if published != "" {
		out["published"] = published
	}
	return out, nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	}
	return 0, false
}
