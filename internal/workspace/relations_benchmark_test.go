package workspace

import (
	"fmt"
	"testing"
)

func BenchmarkImportResolverLargeProject(b *testing.B) {
	paths := make([]string, 0, 20_001)
	paths = append(paths, "cmd/api/main.go")
	for index := 0; index < 20_000; index++ {
		paths = append(paths, fmt.Sprintf("pkg/%05d/service.go", index))
	}
	resolver := newImportResolver(paths)
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		spec := fmt.Sprintf("example.dev/point/pkg/%05d", iteration%20_000)
		if targets := resolver.resolve("cmd/api/main.go", spec); len(targets) != 1 {
			b.Fatalf("targets=%v", targets)
		}
	}
}
