# Library usage

The CLI is a thin wrapper around `github.com/martinghunt/bakpack`.

```go
package main

import (
	"context"

	"github.com/martinghunt/bakpack"
)

func main() {
	ctx := context.Background()

	annotations, err := bakpack.OpenSource("annotations.tar.xz", "auto", "annotation")
	if err != nil {
		panic(err)
	}
	genomes, err := bakpack.OpenSource("genomes.tar.xz", "auto", "genome")
	if err != nil {
		panic(err)
	}

	err = bakpack.BuildArchive(ctx, bakpack.BuildOptions{
		Annotations: annotations,
		Genomes:     genomes,
		ChunkSize:   25,
		OutputPath:  "annotations.bakpack",
		XZThreads:   1,
	})
	if err != nil {
		panic(err)
	}
}
```

With a combined manifest:

```go
annotations, genomes, err := bakpack.OpenManifestSources("samples.tsv")
if err != nil {
	panic(err)
}
err = bakpack.BuildArchive(ctx, bakpack.BuildOptions{
	Annotations: annotations,
	Genomes:     genomes,
	OutputPath:  "annotations.bakpack",
})
```

Core entry points:

```go
genome, err := bakpack.ReadGenome(sampleID, filename, fastaBytes)
reduced, err := bakpack.ReduceBaktaJSON(originalJSON, genome)
restored, err := bakpack.RestoreBaktaJSON(reduced.ReducedJSON, genome)
err := bakpack.BuildArchive(ctx, bakpack.BuildOptions{...})
err := bakpack.ExtractArchive(ctx, bakpack.ExtractOptions{...})
```

Input sources implement:

```go
type FileSource interface {
	Records(context.Context) ([]FileRecord, error)
	Get(context.Context, sample string) (FileRecord, error)
	Order(context.Context) ([]string, error)
}
```
