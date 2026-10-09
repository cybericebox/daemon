package infrastructure

import (
	r "github.com/cybericebox/daemon/internal/model/resources"
	"math"
	"testing"
)

func TestServiceSizingOverflowNeverWrapsToSmallRequest(t *testing.T) {
	s := GroupPodSizing{Base: r.Amount{CPUMillicores: math.MaxInt64 - 1, MemoryBytes: math.MaxInt64 - 1}, PerUnit: r.Amount{CPUMillicores: 2, MemoryBytes: 2}}
	v := s.Size(2)
	if v.CPUMillicores != math.MaxInt64 || v.MemoryBytes != math.MaxInt64 {
		t.Fatal(v)
	}
}
