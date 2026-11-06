package main

import (
	"fmt"
	"math/big"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

const defaultMaxTargets = 2000000

type addressRange struct{ First, Last netip.Addr }
type targetPlan struct {
	Ranges []addressRange
	Ports  []int
	Count  int
}

func maxTargets(c Config) int {
	if c.MaxTargets == 0 {
		return defaultMaxTargets
	}
	return c.MaxTargets
}
func planTargets(c Config) (*targetPlan, error) {
	if e := validateConfig(c); e != nil {
		return nil, e
	}
	plan := &targetPlan{}
	seenPorts := map[int]bool{}
	for _, s := range strings.Split(c.Ports, ",") {
		p, e := strconv.Atoi(strings.TrimSpace(s))
		if e != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("端口无效")
		}
		if !seenPorts[p] {
			plan.Ports = append(plan.Ports, p)
			seenPorts[p] = true
		}
	}
	if len(plan.Ports) > 16 {
		return nil, fmt.Errorf("最多16个端口")
	}
	for _, line := range strings.Split(strings.TrimPrefix(c.Targets, "\ufeff"), "\n") {
		line = strings.SplitN(line, "#", 2)[0]
		for _, s := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' }) {
			if a, e := netip.ParseAddr(s); e == nil {
				if a.Zone() != "" {
					return nil, fmt.Errorf("不支持带zone的IP")
				}
				a = a.Unmap()
				plan.Ranges = append(plan.Ranges, addressRange{a, a})
				continue
			}
			p, e := netip.ParsePrefix(s)
			if e != nil {
				return nil, fmt.Errorf("无效IP/CIDR：%s", s)
			}
			p = p.Masked()
			if p.Addr().Is4In6() && p.Bits() >= 96 {
				p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
			}
			last := p.Addr().AsSlice()
			for bit := p.Bits(); bit < len(last)*8; bit++ {
				last[bit/8] |= 1 << uint(7-bit%8)
			}
			end, _ := netip.AddrFromSlice(last)
			plan.Ranges = append(plan.Ranges, addressRange{p.Addr(), end})
		}
	}
	if len(plan.Ranges) == 0 {
		return nil, fmt.Errorf("请提供云厂商IP或CIDR")
	}
	sort.Slice(plan.Ranges, func(i, j int) bool {
		if v := plan.Ranges[i].First.Compare(plan.Ranges[j].First); v != 0 {
			return v < 0
		}
		return plan.Ranges[i].Last.Compare(plan.Ranges[j].Last) < 0
	})
	merged := make([]addressRange, 0, len(plan.Ranges))
	for _, r := range plan.Ranges {
		if len(merged) == 0 {
			merged = append(merged, r)
			continue
		}
		last := &merged[len(merged)-1]
		adjacent := last.Last.Next()
		if r.First.BitLen() == last.Last.BitLen() && (r.First.Compare(last.Last) <= 0 || adjacent.IsValid() && r.First == adjacent) {
			if r.Last.Compare(last.Last) > 0 {
				last.Last = r.Last
			}
		} else {
			merged = append(merged, r)
		}
	}
	plan.Ranges = merged
	total := new(big.Int)
	limit := big.NewInt(int64(maxTargets(c)))
	ports := big.NewInt(int64(len(plan.Ports)))
	for _, r := range merged {
		count := new(big.Int).Sub(new(big.Int).SetBytes(r.Last.AsSlice()), new(big.Int).SetBytes(r.First.AsSlice()))
		count.Add(count, big.NewInt(1))
		count.Mul(count, ports)
		total.Add(total, count)
		if total.Cmp(limit) > 0 {
			return nil, fmt.Errorf("目标超过当前任务上限%d个IP:端口；可提高max_targets后重试", maxTargets(c))
		}
	}
	plan.Count = int(total.Int64())
	return plan, nil
}
func (p *targetPlan) each(yield func(Target) bool) {
	for _, r := range p.Ranges {
		for a := r.First; ; a = a.Next() {
			ip := a.String()
			for _, port := range p.Ports {
				if !yield(Target{ip, port}) {
					return
				}
			}
			if a == r.Last {
				break
			}
		}
	}
}
func (p *targetPlan) expanded() []Target {
	out := make([]Target, 0, p.Count)
	p.each(func(t Target) bool { out = append(out, t); return true })
	return out
}
