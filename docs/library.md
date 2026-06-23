# Library usage

The CLI is a thin wrapper around `github.com/martinghunt/bakpack`.

## Extract annotations

Open a `.bakpack` archive once, then extract one or many samples. The archive
path can be local or an HTTP(S) URL with byte-range support.

```go
package main

import (
	"context"

	"github.com/martinghunt/bakpack"
)

func main() {
	ctx := context.Background()

	genomes, err := bakpack.OpenSource("genomes.agc", "agc", "genome")
	if err != nil {
		panic(err)
	}
	archive, err := bakpack.OpenArchive(ctx, "https://example.org/annotations.bakpack")
	if err != nil {
		panic(err)
	}
	defer archive.Close()

	results, err := archive.Extract(ctx, bakpack.ExtractRequest{
		Genomes:  genomes,
		Samples:  []string{"sampleA", "sampleB"},
		Original: true,
		Genome:   true,
	})
	if err != nil {
		panic(err)
	}
	for _, result := range results {
		_ = result.OriginalJSON
		_ = result.GenomeFASTA
	}
}
```

For large requests, use `OnSample` to handle each extracted sample without
accumulating all result bytes in memory:

```go
_, err = archive.Extract(ctx, bakpack.ExtractRequest{
	Genomes:  genomes,
	Samples:  samples,
	Original: true,
	OnSample: func(result bakpack.ExtractedSample) error {
		// write result.OriginalJSON, send it to another package, etc.
		return nil
	},
})
```

During extraction, non-tar genome sources such as `.agc`, directories, manifests,
and file lists are fetched one sample at a time. `.tar.xz` genome sources are
streamed once and selected genomes are kept in memory, which avoids repeatedly
decompressing the same tar archive.

Pass a custom HTTP client when the remote archive needs specific timeouts,
headers, transport settings, or authentication:

```go
archive, err := bakpack.OpenArchive(ctx, archiveURL, bakpack.OpenArchiveOptions{
	HTTPClient: client,
})
```

## Build archives

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
archive, err := bakpack.OpenArchive(ctx, archivePath)
results, err := archive.Extract(ctx, bakpack.ExtractRequest{...})
index, err := bakpack.ReadArchiveIndexContext(ctx, archivePath)
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
