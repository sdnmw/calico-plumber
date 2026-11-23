#ifndef __VMLINUX_MIN_H__
#define __VMLINUX_MIN_H__
#define __VMLINUX_H__

typedef unsigned char __u8;
typedef short int __s16;
typedef short unsigned int __u16;
typedef int __s32;
typedef unsigned int __u32;
typedef long long int __s64;
typedef long long unsigned int __u64;

typedef __u16 __be16;
typedef __u32 __be32;
typedef __u32 __wsum;
typedef __u8 u8;
typedef __u16 u16;
typedef __u32 u32;
typedef __u64 u64;
typedef long unsigned int size_t;

#define AF_INET 2
#define IPPROTO_TCP 6
#define IPPROTO_UDP 17
#define BPF_ANY 0
#define BPF_NOEXIST 1

enum bpf_map_type {
	BPF_MAP_TYPE_UNSPEC = 0,
	BPF_MAP_TYPE_HASH = 1,
	BPF_MAP_TYPE_ARRAY = 2,
	BPF_MAP_TYPE_LRU_HASH = 9,
};

#if defined(__TARGET_ARCH_x86)
struct pt_regs {
	unsigned long r15;
	unsigned long r14;
	unsigned long r13;
	unsigned long r12;
	unsigned long bp;
	unsigned long bx;
	unsigned long r11;
	unsigned long r10;
	unsigned long r9;
	unsigned long r8;
	unsigned long ax;
	unsigned long cx;
	unsigned long dx;
	unsigned long si;
	unsigned long di;
	unsigned long orig_ax;
	unsigned long ip;
	unsigned long cs;
	unsigned long flags;
	unsigned long sp;
	unsigned long ss;
};
#elif defined(__TARGET_ARCH_arm64)
struct user_pt_regs {
	__u64 regs[31];
	__u64 sp;
	__u64 pc;
	__u64 pstate;
};
struct pt_regs {
	union {
		struct user_pt_regs user_regs;
		struct {
			__u64 regs[31];
			__u64 sp;
			__u64 pc;
			__u64 pstate;
		};
	};
};
#endif

struct in_addr {
	__be32 s_addr;
} __attribute__((preserve_access_index));

struct sockaddr_in {
	__u16 sin_family;
	__be16 sin_port;
	struct in_addr sin_addr;
	unsigned char __pad[8];
} __attribute__((preserve_access_index));

struct msghdr {
	void *msg_name;
	int msg_namelen;
} __attribute__((preserve_access_index));

struct sock_common {
	union {
		struct {
			__be32 skc_daddr;
			__be32 skc_rcv_saddr;
		};
	};
	union {
		struct {
			__be16 skc_dport;
			__u16 skc_num;
		};
	};
	short unsigned int skc_family;
} __attribute__((preserve_access_index));

struct sock {
	struct sock_common __sk_common;
} __attribute__((preserve_access_index));

struct inet_sock {
	struct sock sk;
	__be32 inet_saddr;
	__be16 inet_sport;
} __attribute__((preserve_access_index));

struct tcp_sock {
	__u32 srtt_us;
} __attribute__((preserve_access_index));

struct sk_buff {
	unsigned int len;
} __attribute__((preserve_access_index));

#endif /* __VMLINUX_MIN_H__ */
