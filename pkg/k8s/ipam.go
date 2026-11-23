package k8s

import (
	"encoding/binary"
	"math"
	"net"
	"sort"
	"strings"
	"time"

	"calico-netdiag/pkg/model"
)

// IPLookupMeta maps an IP to its Calico IPPool and IPAMBlock metadata.
type IPLookupMeta struct {
	PoolName  string
	BlockName string
	BlockCIDR string
	Borrowed  bool
	EncapMode model.CalicoEncapMode
}

// BuildIPAMSnapshot decodes Calico IPPools, IPAMBlocks, and BlockAffinities and correlates
// every allocated slot with live Kubernetes Pods.
func BuildIPAMSnapshot(
	rawPools []RawIPPool,
	rawBlocks []RawIPAMBlock,
	rawAffinities []RawBlockAffinity,
	podsByIP map[string]model.PodBrief,
	podsByKey map[string]model.PodBrief,
	bgpEnabled bool,
) (model.IPAMSnapshot, map[string]IPLookupMeta) {
	// Map block CIDR -> affinity state
	affState := make(map[string]string, len(rawAffinities))
	for _, a := range rawAffinities {
		if !a.Deleted {
			affState[a.CIDR] = a.State
		}
	}

	// Build pool summaries + parsed CIDRs
	type poolTrack struct {
		summary model.IPPoolSummary
		netCIDR *net.IPNet
	}
	tracks := make([]*poolTrack, 0, len(rawPools))
	for _, rp := range rawPools {
		_, cidrNet, err := net.ParseCIDR(rp.CIDR)
		if err != nil {
			continue
		}
		ones, bits := cidrNet.Mask.Size()
		totalIPs := 0
		totalBlocks := 0
		if bits == 32 && ones <= 32 {
			totalIPs = 1 << uint(32-ones)
			if rp.BlockSize >= ones && rp.BlockSize <= 32 {
				totalBlocks = 1 << uint(rp.BlockSize-ones)
			}
		}
		encap := ClassifyEncapMode(rp.IPIPMode, rp.VXLANMode, bgpEnabled)
		tracks = append(tracks, &poolTrack{
			netCIDR: cidrNet,
			summary: model.IPPoolSummary{
				Name:             rp.Name,
				CIDR:             rp.CIDR,
				BlockSize:        rp.BlockSize,
				IPIPMode:         rp.IPIPMode,
				VXLANMode:        rp.VXLANMode,
				EncapMode:        encap,
				NATOutgoing:      rp.NATOutgoing,
				Disabled:         rp.Disabled,
				DisableBGPExport: rp.DisableBGPExport,
				NodeSelector:     rp.NodeSelector,
				AllowedUses:      rp.AllowedUses,
				TotalIPs:         totalIPs,
				TotalBlocks:      totalBlocks,
			},
		})
	}

	ipLookup := make(map[string]IPLookupMeta, 256)
	blocksOut := make([]model.IPAMBlockDetail, 0, len(rawBlocks))

	for _, rb := range rawBlocks {
		if rb.Deleted {
			continue
		}
		baseIP, blockNet, err := net.ParseCIDR(rb.CIDR)
		if err != nil || baseIP.To4() == nil {
			continue
		}
		baseUint := binary.BigEndian.Uint32(baseIP.To4())
		ones, _ := blockNet.Mask.Size()
		totalSlots := 1 << uint(32-ones)
		if len(rb.Allocations) > 0 && len(rb.Allocations) < totalSlots {
			totalSlots = len(rb.Allocations)
		}

		// Find parent IPPool
		var parent *poolTrack
		for _, pt := range tracks {
			if pt.netCIDR.Contains(baseIP) {
				parent = pt
				break
			}
		}
		poolName := ""
		encapMode := model.EncapBGPDirect
		if parent != nil {
			poolName = parent.summary.Name
			encapMode = parent.summary.EncapMode
			parent.summary.AllocatedBlocks++
		}

		affinityNode := strings.TrimPrefix(rb.Affinity, "host:")
		state := affState[rb.CIDR]
		if state == "" {
			if affinityNode != "" {
				state = "confirmed"
			} else {
				state = "none"
			}
		}

		detail := model.IPAMBlockDetail{
			Name:           rb.Name,
			CIDR:           rb.CIDR,
			PoolName:       poolName,
			AffinityNode:   affinityNode,
			AffinityState:  state,
			StrictAffinity: rb.StrictAffinity,
			TotalSlots:     totalSlots,
			Slots:          make([]model.IPSlotDetail, totalSlots),
		}

		for i := 0; i < totalSlots; i++ {
			ipBytes := make([]byte, 4)
			binary.BigEndian.PutUint32(ipBytes, baseUint+uint32(i))
			ipStr := net.IP(ipBytes).String()

			slot := model.IPSlotDetail{
				Index:  i,
				IP:     ipStr,
				Status: model.IPStatusFree,
			}

			var attrIdx *int
			if i < len(rb.Allocations) {
				attrIdx = rb.Allocations[i]
			}
			if attrIdx != nil && *attrIdx >= 0 && *attrIdx < len(rb.Attributes) {
				attr := rb.Attributes[*attrIdx]
				slot.HandleID = attr.HandleID
				slot.Namespace = attr.Namespace
				slot.PodName = attr.Pod
				slot.PodNode = attr.Node
				slot.AllocatedAt = attr.Timestamp

				detail.AllocatedSlots++
				if parent != nil {
					parent.summary.AllocatedIPs++
				}

				if isTunnelHandle(attr.HandleID, attr.Type) {
					slot.Status = model.IPStatusTunnel
					if parent != nil {
						parent.summary.TunnelIPs++
					}
				} else {
					// Correlate with live K8s Pod
					pod, found := podsByIP[ipStr]
					if !found && attr.Namespace != "" && attr.Pod != "" {
						pod, found = podsByKey[attr.Namespace+"/"+attr.Pod]
					}
					if found {
						slot.Namespace = pod.Namespace
						slot.PodName = pod.Name
						slot.PodNode = pod.NodeName
						slot.WorkloadKind = pod.WorkloadKind
						slot.WorkloadName = pod.WorkloadName
						slot.PodPhase = pod.Phase
						borrowed := affinityNode != "" && pod.NodeName != "" && pod.NodeName != affinityNode
						if borrowed {
							slot.Status = model.IPStatusBorrowed
							detail.BorrowedSlots++
							if parent != nil {
								parent.summary.BorrowedIPs++
							}
						} else {
							slot.Status = model.IPStatusPod
							if parent != nil {
								parent.summary.ActivePodIPs++
							}
						}
						ipLookup[ipStr] = IPLookupMeta{
							PoolName:  poolName,
							BlockName: rb.Name,
							BlockCIDR: rb.CIDR,
							Borrowed:  borrowed,
							EncapMode: encapMode,
						}
					} else {
						slot.Status = model.IPStatusOrphan
						detail.OrphanSlots++
						if parent != nil {
							parent.summary.OrphanIPs++
						}
						ipLookup[ipStr] = IPLookupMeta{
							PoolName:  poolName,
							BlockName: rb.Name,
							BlockCIDR: rb.CIDR,
							Borrowed:  false,
							EncapMode: encapMode,
						}
					}
				}
			}

			detail.Slots[i] = slot
		}

		if detail.TotalSlots > 0 {
			detail.UtilizationPercent = round2(float64(detail.AllocatedSlots) * 100.0 / float64(detail.TotalSlots))
		}
		blocksOut = append(blocksOut, detail)
	}

	// Sort blocks by affinity node then CIDR
	sort.Slice(blocksOut, func(i, j int) bool {
		if blocksOut[i].AffinityNode != blocksOut[j].AffinityNode {
			return blocksOut[i].AffinityNode < blocksOut[j].AffinityNode
		}
		return blocksOut[i].CIDR < blocksOut[j].CIDR
	})

	poolsOut := make([]model.IPPoolSummary, 0, len(tracks))
	for _, pt := range tracks {
		if pt.summary.TotalIPs > 0 {
			pt.summary.UtilizationPercent = round2(float64(pt.summary.AllocatedIPs) * 100.0 / float64(pt.summary.TotalIPs))
		}
		poolsOut = append(poolsOut, pt.summary)
	}
	sort.Slice(poolsOut, func(i, j int) bool { return poolsOut[i].Name < poolsOut[j].Name })

	return model.IPAMSnapshot{
		UpdatedAt: time.Now(),
		Pools:     poolsOut,
		Blocks:    blocksOut,
	}, ipLookup
}

func isTunnelHandle(handleID, attrType string) bool {
	h := strings.ToLower(handleID)
	t := strings.ToLower(attrType)
	return strings.Contains(h, "ipip-tunnel") ||
		strings.Contains(h, "vxlan-tunnel") ||
		strings.Contains(h, "wireguard-tunnel") ||
		strings.Contains(t, "tunnel")
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
