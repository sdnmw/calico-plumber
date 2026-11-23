//go:build ignore

#include "vmlinux_min.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

char __license[] SEC("license") = "Dual MIT/GPL";

struct flow_key {
	__u32 src_ip;
	__u32 dst_ip;
	__u16 src_port;
	__u16 dst_port;
	__u8  proto;
	__u8  pad[3];
};

struct flow_metrics {
	__u64 tx_bytes;
	__u64 rx_bytes;
	__u64 tx_packets;
	__u64 rx_packets;
	__u64 srtt_us_sum;
	__u32 srtt_samples;
	__u32 srtt_max_us;
	__u32 retransmits;
	__u32 resets;
	__u64 last_seen_ns;
};

struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 65536);
	__type(key, struct flow_key);
	__type(value, struct flow_metrics);
} flow_stats SEC(".maps");

static __always_inline int is_loopback_or_zero(__u32 ip) {
	if (ip == 0)
		return 1;
	/* In network byte order, 127.x.x.x has lowest byte == 127 on little-endian */
	if ((ip & 0xff) == 127)
		return 1;
	return 0;
}

static __always_inline int fill_key_from_sock(struct sock *sk, __u8 proto, struct flow_key *key) {
	__u16 family = BPF_CORE_READ(sk, __sk_common.skc_family);
	if (family != AF_INET)
		return -1;

	__u32 saddr = BPF_CORE_READ(sk, __sk_common.skc_rcv_saddr);
	__u32 daddr = BPF_CORE_READ(sk, __sk_common.skc_daddr);
	__u16 sport = BPF_CORE_READ(sk, __sk_common.skc_num);
	__u16 dport = bpf_ntohs(BPF_CORE_READ(sk, __sk_common.skc_dport));

	if (is_loopback_or_zero(saddr) || is_loopback_or_zero(daddr))
		return -1;
	if (sport == 0 && dport == 0)
		return -1;

	key->src_ip = saddr;
	key->dst_ip = daddr;
	key->src_port = sport;
	key->dst_port = dport;
	key->proto = proto;
	key->pad[0] = 0;
	key->pad[1] = 0;
	key->pad[2] = 0;
	return 0;
}

static __always_inline struct flow_metrics *get_or_init_metrics(struct flow_key *key) {
	struct flow_metrics *m = bpf_map_lookup_elem(&flow_stats, key);
	if (m)
		return m;
	struct flow_metrics init = {};
	init.last_seen_ns = bpf_ktime_get_ns();
	bpf_map_update_elem(&flow_stats, key, &init, BPF_NOEXIST);
	return bpf_map_lookup_elem(&flow_stats, key);
}

SEC("kprobe/tcp_sendmsg")
int BPF_KPROBE(kprobe_tcp_sendmsg, struct sock *sk, struct msghdr *msg, size_t size) {
	if (!sk || size == 0)
		return 0;
	struct flow_key key = {};
	if (fill_key_from_sock(sk, IPPROTO_TCP, &key) < 0)
		return 0;

	struct flow_metrics *m = get_or_init_metrics(&key);
	if (!m)
		return 0;

	__sync_fetch_and_add(&m->tx_bytes, (__u64)size);
	__sync_fetch_and_add(&m->tx_packets, 1);
	m->last_seen_ns = bpf_ktime_get_ns();
	return 0;
}

SEC("kprobe/tcp_cleanup_rbuf")
int BPF_KPROBE(kprobe_tcp_cleanup_rbuf, struct sock *sk, int copied) {
	if (!sk || copied <= 0)
		return 0;
	struct flow_key key = {};
	if (fill_key_from_sock(sk, IPPROTO_TCP, &key) < 0)
		return 0;

	struct flow_metrics *m = get_or_init_metrics(&key);
	if (!m)
		return 0;

	__sync_fetch_and_add(&m->rx_bytes, (__u64)copied);
	__sync_fetch_and_add(&m->rx_packets, 1);
	m->last_seen_ns = bpf_ktime_get_ns();
	return 0;
}

SEC("kprobe/tcp_rcv_established")
int BPF_KPROBE(kprobe_tcp_rcv_established, struct sock *sk, struct sk_buff *skb) {
	if (!sk)
		return 0;
	struct flow_key key = {};
	if (fill_key_from_sock(sk, IPPROTO_TCP, &key) < 0)
		return 0;

	struct tcp_sock *tp = (struct tcp_sock *)sk;
	__u32 srtt = BPF_CORE_READ(tp, srtt_us) >> 3;
	if (srtt == 0)
		return 0;

	struct flow_metrics *m = get_or_init_metrics(&key);
	if (!m)
		return 0;

	__sync_fetch_and_add(&m->srtt_us_sum, (__u64)srtt);
	__sync_fetch_and_add(&m->srtt_samples, 1);
	if (srtt > m->srtt_max_us)
		m->srtt_max_us = srtt;
	m->last_seen_ns = bpf_ktime_get_ns();
	return 0;
}

SEC("kprobe/tcp_retransmit_skb")
int BPF_KPROBE(kprobe_tcp_retransmit_skb, struct sock *sk) {
	if (!sk)
		return 0;
	struct flow_key key = {};
	if (fill_key_from_sock(sk, IPPROTO_TCP, &key) < 0)
		return 0;

	struct flow_metrics *m = get_or_init_metrics(&key);
	if (!m)
		return 0;

	__sync_fetch_and_add(&m->retransmits, 1);
	m->last_seen_ns = bpf_ktime_get_ns();
	return 0;
}

SEC("kprobe/tcp_reset")
int BPF_KPROBE(kprobe_tcp_reset, struct sock *sk) {
	if (!sk)
		return 0;
	struct flow_key key = {};
	if (fill_key_from_sock(sk, IPPROTO_TCP, &key) < 0)
		return 0;

	struct flow_metrics *m = get_or_init_metrics(&key);
	if (!m)
		return 0;

	__sync_fetch_and_add(&m->resets, 1);
	m->last_seen_ns = bpf_ktime_get_ns();
	return 0;
}

SEC("kprobe/udp_sendmsg")
int BPF_KPROBE(kprobe_udp_sendmsg, struct sock *sk, struct msghdr *msg, size_t len) {
	if (!sk || len == 0)
		return 0;
	__u16 family = BPF_CORE_READ(sk, __sk_common.skc_family);
	if (family != AF_INET)
		return 0;

	__u32 saddr = BPF_CORE_READ(sk, __sk_common.skc_rcv_saddr);
	if (saddr == 0) {
		struct inet_sock *isk = (struct inet_sock *)sk;
		saddr = BPF_CORE_READ(isk, inet_saddr);
	}
	__u32 daddr = BPF_CORE_READ(sk, __sk_common.skc_daddr);
	__u16 sport = BPF_CORE_READ(sk, __sk_common.skc_num);
	__u16 dport = bpf_ntohs(BPF_CORE_READ(sk, __sk_common.skc_dport));

	if (daddr == 0 && msg != 0) {
		struct sockaddr_in *sin = (struct sockaddr_in *)BPF_CORE_READ(msg, msg_name);
		if (sin != 0) {
			daddr = BPF_CORE_READ(sin, sin_addr.s_addr);
			dport = bpf_ntohs(BPF_CORE_READ(sin, sin_port));
		}
	}

	if (is_loopback_or_zero(saddr) || is_loopback_or_zero(daddr))
		return 0;

	struct flow_key key = {
		.src_ip = saddr,
		.dst_ip = daddr,
		.src_port = sport,
		.dst_port = dport,
		.proto = IPPROTO_UDP,
	};

	struct flow_metrics *m = get_or_init_metrics(&key);
	if (!m)
		return 0;

	__sync_fetch_and_add(&m->tx_bytes, (__u64)len);
	__sync_fetch_and_add(&m->tx_packets, 1);
	m->last_seen_ns = bpf_ktime_get_ns();
	return 0;
}

SEC("kprobe/skb_consume_udp")
int BPF_KPROBE(kprobe_skb_consume_udp, struct sock *sk, struct sk_buff *skb, int len) {
	if (!sk || len <= 0)
		return 0;
	struct flow_key key = {};
	if (fill_key_from_sock(sk, IPPROTO_UDP, &key) < 0)
		return 0;

	struct flow_metrics *m = get_or_init_metrics(&key);
	if (!m)
		return 0;

	__sync_fetch_and_add(&m->rx_bytes, (__u64)len);
	__sync_fetch_and_add(&m->rx_packets, 1);
	m->last_seen_ns = bpf_ktime_get_ns();
	return 0;
}
