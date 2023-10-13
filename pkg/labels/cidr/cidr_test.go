// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

//go:build !privileged_tests
// +build !privileged_tests

package cidr

import (
	"net"
	"net/netip"
	"runtime"
	"testing"

	"github.com/hashicorp/golang-lru/v2/simplelru"
	. "gopkg.in/check.v1"

	"github.com/cilium/cilium/pkg/checker"
	"github.com/cilium/cilium/pkg/labels"
)

// Hook up gocheck into the "go test" runner.
func Test(t *testing.T) {
	TestingT(t)
}

type CIDRLabelsSuite struct{}

var _ = Suite(&CIDRLabelsSuite{})

// TestGetCIDRLabels checks that GetCIDRLabels returns a sane set of labels for
// given CIDRs.
func (s *CIDRLabelsSuite) TestGetCIDRLabels(c *C) {
	_, cidr, err := net.ParseCIDR("192.0.2.3/32")
	c.Assert(err, IsNil)
	expected := labels.ParseLabelArray(
		"cidr:0.0.0.0/0",
		"cidr:128.0.0.0/1",
		"cidr:192.0.0.0/8",
		"cidr:192.0.2.0/24",
		"cidr:192.0.2.3/32",
		"reserved:world",
	)

	lbls := GetCIDRLabels(cidr)
	lblArray := lbls.LabelArray()
	c.Assert(lblArray.Lacks(expected), checker.DeepEquals, labels.LabelArray{})
	// IPs should be masked as the labels are generated
	c.Assert(lblArray.Has("cidr:192.0.2.3/24"), Equals, false)

	_, cidr, err = net.ParseCIDR("192.0.2.0/24")
	c.Assert(err, IsNil)
	expected = labels.ParseLabelArray(
		"cidr:0.0.0.0/0",
		"cidr:192.0.2.0/24",
		"reserved:world",
	)

	lbls = GetCIDRLabels(cidr)
	lblArray = lbls.LabelArray()
	c.Assert(lblArray.Lacks(expected), checker.DeepEquals, labels.LabelArray{})
	// CIDRs that are covered by the prefix should not be in the labels
	c.Assert(lblArray.Has("cidr.192.0.2.3/32"), Equals, false)

	// Zero-length prefix / default route should become reserved:world.
	_, cidr, err = net.ParseCIDR("0.0.0.0/0")
	c.Assert(err, IsNil)
	expected = labels.ParseLabelArray(
		"reserved:world",
	)

	lbls = GetCIDRLabels(cidr)
	lblArray = lbls.LabelArray()
	c.Assert(lblArray.Lacks(expected), checker.DeepEquals, labels.LabelArray{})
	c.Assert(lblArray.Has("cidr.0.0.0.0/0"), Equals, false)

	// Note that we convert the colons in IPv6 addresses into dashes when
	// translating into labels, because endpointSelectors don't support
	// colons.
	_, cidr, err = net.ParseCIDR("2001:DB8::1/128")
	c.Assert(err, IsNil)
	expected = labels.ParseLabelArray(
		"cidr:0--0/0",
		"cidr:2000--0/3",
		"cidr:2001--0/16",
		"cidr:2001-d00--0/24",
		"cidr:2001-db8--0/32",
		"cidr:2001-db8--1/128",
		"reserved:world",
	)

	lbls = GetCIDRLabels(cidr)
	lblArray = lbls.LabelArray()
	c.Assert(lblArray.Lacks(expected), checker.DeepEquals, labels.LabelArray{})
	// IPs should be masked as the labels are generated
	c.Assert(lblArray.Has("cidr.2001-db8--1/24"), Equals, false)
}

// TestGetCIDRLabelsInCluster checks that the cluster label is properly added
// when getting labels for CIDRs that are equal to or within the cluster range.
func (s *CIDRLabelsSuite) TestGetCIDRLabelsInCluster(c *C) {
	_, cidr, err := net.ParseCIDR("10.0.0.0/16")
	c.Assert(err, IsNil)
	expected := labels.ParseLabelArray(
		"cidr:0.0.0.0/0",
		"cidr:10.0.0.0/16",
		"reserved:world",
	)
	lbls := GetCIDRLabels(cidr)
	lblArray := lbls.LabelArray()
	c.Assert(lblArray.Lacks(expected), checker.DeepEquals, labels.LabelArray{})

	// This case is firmly within the cluster range
	_, cidr, err = net.ParseCIDR("2001:db8:cafe::cab:4:b0b:0/112")
	c.Assert(err, IsNil)
	expected = labels.ParseLabelArray(
		"cidr:0--0/0",
		"cidr:2001-db8-cafe--0/64",
		"cidr:2001-db8-cafe-0-cab-4--0/96",
		"cidr:2001-db8-cafe-0-cab-4-b0b-0/112",
		"reserved:world",
	)
	lbls = GetCIDRLabels(cidr)
	lblArray = lbls.LabelArray()
	c.Assert(lblArray.Lacks(expected), checker.DeepEquals, labels.LabelArray{})
}

func (s *CIDRLabelsSuite) TestIPStringToLabel(c *C) {
	for _, tc := range []struct {
		ip      string
		label   string
		wantErr bool
	}{
		{
			ip:    "0.0.0.0/0",
			label: "cidr:0.0.0.0/0",
		},
		{
			ip:    "192.0.2.3",
			label: "cidr:192.0.2.3/32",
		},
		{
			ip:    "192.0.2.3/32",
			label: "cidr:192.0.2.3/32",
		},
		{
			ip:    "192.0.2.3/24",
			label: "cidr:192.0.2.0/24",
		},
		{
			ip:    "192.0.2.0/24",
			label: "cidr:192.0.2.0/24",
		},
		{
			ip:    "::/0",
			label: "cidr:0--0/0",
		},
		{
			ip:    "fdff::ff",
			label: "cidr:fdff--ff/128",
		},
		{
			ip:    "f00d:42::ff/128",
			label: "cidr:f00d-42--ff/128",
		},
		{
			ip:    "f00d:42::ff/96",
			label: "cidr:f00d-42--0/96",
		},
		{
			ip:      "",
			wantErr: true,
		},
		{
			ip:      "foobar",
			wantErr: true,
		},
	} {
		lbl, err := IPStringToLabel(tc.ip)
		if !tc.wantErr {
			c.Assert(err, IsNil)
			c.Assert(lbl.String(), checker.DeepEquals, tc.label)
		} else {
			c.Assert(err, Not(IsNil))
		}
	}
}

func mustCIDR(cidr string) *net.IPNet {
	_, c, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return c
}

func BenchmarkGetCIDRLabels(b *testing.B) {
	// clear the cache
	cidrLabelsCache, _ = simplelru.NewLRU[netip.Prefix, []labels.Label](cidrLabelsCacheMaxSize, nil)

	for _, cidr := range []*net.IPNet{
		mustCIDR("0.0.0.0/0"),
		mustCIDR("10.16.0.0/16"),
		mustCIDR("192.0.2.3/32"),
		mustCIDR("192.0.2.3/24"),
		mustCIDR("192.0.2.0/24"),
		mustCIDR("::/0"),
		mustCIDR("fdff::ff/128"),
		mustCIDR("f00d:42::ff/128"),
		mustCIDR("f00d:42::ff/96"),
	} {
		b.Run(cidr.String(), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = GetCIDRLabels(cidr)
			}
		})
	}
}

// BenchmarkCIDRLabelsCacheHeapUsageIPv4 should be run with -benchtime=1x
func BenchmarkCIDRLabelsCacheHeapUsageIPv4(b *testing.B) {
	b.Skip()

	// clear the cache
	cidrLabelsCache, _ = simplelru.NewLRU[netip.Prefix, []labels.Label](cidrLabelsCacheMaxSize, nil)

	// be sure to fill the cache
	prefixes := make([]*net.IPNet, 0, 256*256)
	octets := [4]byte{0, 0, 1, 1}
	for i := 0; i < 256*256; i++ {
		octets[0], octets[1] = byte(i/256), byte(i%256)
		prefix := netip.PrefixFrom(netip.AddrFrom4(octets), 32)
		prefixes = append(prefixes, mustCIDR(prefix.String()))
	}

	var m1, m2 runtime.MemStats
	// One GC does not give precise results,
	// because concurrent sweep may be still in progress.
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m1)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, cidr := range prefixes {
			_ = GetCIDRLabels(cidr)
		}
	}
	b.StopTimer()

	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m2)

	usage := m2.HeapAlloc - m1.HeapAlloc
	b.Logf("Memoization map heap usage: %.2f KiB", float64(usage)/1024)
}

// BenchmarkCIDRLabelsCacheHeapUsageIPv6 should be run with -benchtime=1x
func BenchmarkCIDRLabelsCacheHeapUsageIPv6(b *testing.B) {
	b.Skip()

	// clear the cache
	cidrLabelsCache, _ = simplelru.NewLRU[netip.Prefix, []labels.Label](cidrLabelsCacheMaxSize, nil)

	// be sure to fill the cache
	prefixes := make([]*net.IPNet, 0, 256*256)
	octets := [16]byte{
		0x00, 0x00, 0x00, 0xd8, 0x33, 0x33, 0x44, 0x44,
		0x55, 0x55, 0x66, 0x66, 0x77, 0x77, 0x88, 0x88,
	}
	for i := 0; i < 256*256; i++ {
		octets[15], octets[14] = byte(i/256), byte(i%256)
		prefix := netip.PrefixFrom(netip.AddrFrom16(octets), 128)
		prefixes = append(prefixes, mustCIDR(prefix.String()))
	}

	var m1, m2 runtime.MemStats
	// One GC does not give precise results,
	// because concurrent sweep may be still in progress.
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m1)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, cidr := range prefixes {
			_ = GetCIDRLabels(cidr)
		}
	}
	b.StopTimer()

	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m2)

	usage := m2.HeapAlloc - m1.HeapAlloc
	b.Logf("Memoization map heap usage: %.2f KiB", float64(usage)/1024)
}

func BenchmarkIPStringToLabel(b *testing.B) {
	for _, ip := range []string{
		"0.0.0.0/0",
		"192.0.2.3",
		"192.0.2.3/32",
		"192.0.2.3/24",
		"192.0.2.0/24",
		"::/0",
		"fdff::ff",
		"f00d:42::ff/128",
		"f00d:42::ff/96",
	} {
		b.Run(ip, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := IPStringToLabel(ip)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
