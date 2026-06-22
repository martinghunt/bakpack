# bakpack

`bakpack` is a Go command line tool and library for compressing Bakta JSON annotation files while keeping them reconstructable and checkable against the original JSON content.

This repository was developed with substantial coding assistance from [OpenAI Codex](https://openai.com/codex), which helped with implementation, refactoring, tests, documentation, and benchmarking under human direction and review.

## Install

The simplest way to install `bakpack` is to download the latest prebuilt binary from the GitHub releases page:

- https://github.com/martinghunt/bakpack/releases/latest

Choose the archive or binary matching your OS and CPU architecture.
Release pages also include a SHA-256 checksum file named like `bakpack-v0.1.0-checksums.txt`.
Use it to verify downloaded archives before installing.

After installing, check the version with:

```bash
bakpack --version
```

If you want to build locally instead:

```bash
git clone https://github.com/martinghunt/bakpack.git
cd bakpack
./build.sh
```

The binary is written to:

```text
build/bakpack
```

With Go directly:

```bash
go install github.com/martinghunt/bakpack/cmd/bakpack@latest
```

Archive creation uses the command line `xz` program by default:

```text
xz -9e -T1 -c
```

Put `xz` in `PATH` for best compression. Passing `--go-xz` uses the pure-Go xz implementation instead, but it is expected to compress worse. AGC genome input requires `agc` in `PATH`.

## Quick Start

Make one reduced JSON file:

```bash
bakpack reduce sample.bakta.json sample.fa -o sample.reduced.bakta.json
```

Reconstruct original JSON content:

```bash
bakpack restore sample.reduced.bakta.json sample.fa -o sample.bakta.json
```

Build a compressed archive:

```bash
bakpack build \
  --annotations annotations.tar.xz \
  --genomes genomes.tar.xz \
  --output annotations.bakpack
```

Extract one reconstructed annotation and its genome FASTA:

```bash
bakpack extract annotations.bakpack SAMN1 \
  --genomes genomes.tar.xz \
  --original \
  --genome \
  --output-dir out
```

The archive path can also be an HTTP(S) URL when the server supports byte-range requests:

```bash
bakpack extract https://example.org/annotations.bakpack SAMN1 \
  --genomes genomes.tar.xz \
  --original \
  --output-dir out
```

Extract several reduced annotations efficiently:

```bash
bakpack extract annotations.bakpack SAMN1 SAMN2 SAMN3 \
  --reduced \
  --output-dir out
```

## What Gets Removed

`bakpack reduce` removes fields that can be reconstructed from the matching genome sequence:

- `sequences[].sequence`
- feature `nt`
- feature `aa`
- selected derived `stats` values: `no_sequences`, `size`, `n_ratio`, `n50`
- `sequences[].length`
- protein `aa_hexdigest`
- CDS `start_type`
- `hypothetical` when the product is `hypothetical protein`
- gap feature `length`

Reduced JSON embeds the original canonical JSON checksum. Archives also store checksums in the index so extracted reduced JSON and reconstructed original JSON can be verified.

## Archive Format

The current `.bakpack` archive uses:

- a small fixed magic and front index length
- an xz-compressed JSON index
- xz-compressed chunks
- 25 samples per chunk by default
- a specialized columnar chunk payload

Within each chunk, Bakta feature values are stored as typed streams instead of repeated JSON objects. High-volume fields get specialized codecs, including contig indexes and sample-local numeric suffix encoding for `id` and `locus`.

Extraction reads the front index and only decompresses chunks containing requested samples.
For HTTP(S) archive URLs, `bakpack` uses byte-range GET requests for the fixed header, compressed index, and requested chunks.

The detailed binary format is documented in [FORMAT.md](FORMAT.md).

## Command Line

### `reduce`

```bash
bakpack reduce BAKTA_JSON GENOME_FASTA -o REDUCED_JSON
```

Writes reduced JSON and prints original/reduced canonical SHA-256 values to stderr.

### `restore`

```bash
bakpack restore REDUCED_JSON GENOME_FASTA -o BAKTA_JSON
```

This verifies the reconstructed original JSON against the canonical SHA-256 embedded in the reduced JSON.

### `build`

```bash
bakpack build \
  --annotations ANNOTATION_SOURCE \
  --genomes GENOME_SOURCE \
  --output annotations.bakpack
```

Common options:

```text
--annotations-format auto|dir|list|tar.xz
--genomes-format     auto|dir|list|tar.xz|agc
--chunk-size         samples per compressed chunk, default 25
--order              file of sample IDs defining archive order
--xz-threads         threads passed as xz -T, default 1
--xz-command         xz command path, default xz
--xz-arg             repeat to replace default xz args
--go-xz              use pure-Go xz compression
```

Default sample order is the genome source order. This can improve compression when the genome source order groups similar genomes, because related annotations tend to share more structure and repeated values.

### `extract`

```bash
bakpack extract ARCHIVE SAMPLE... [flags]
```

`ARCHIVE` can be a local `.bakpack` path or an HTTP(S) URL. HTTP(S) servers must support byte-range requests.

Output modes:

```text
--reduced    write SAMPLE.reduced.bakta.json
--original   write SAMPLE.bakta.json
--genome     write SAMPLE.fa
```

If no output mode is selected, `--reduced` is used.

Use a sample list file:

```bash
bakpack extract annotations.bakpack \
  --samples-file samples.txt \
  --reduced \
  --output-dir out
```

Original JSON and genome FASTA extraction require a genome source:

```bash
--genomes genomes.tar.xz
--genomes-format auto|dir|list|tar.xz|agc
```

### `index`

```bash
bakpack index annotations.bakpack
```

Prints the archive index JSON. The archive can be a local path or an HTTP(S) URL with byte-range support.

## Input Sources

Annotation sources support:

- directory
- file list
- `.tar.xz`

Genome sources support:

- directory
- file list
- `.tar.xz`
- `.agc`

Sample IDs are inferred from common names:

```text
sample.bakta.json -> sample
sample.json       -> sample
sample.fa         -> sample
sample.fasta      -> sample
sample.fna        -> sample
```

File lists can contain one path per line:

```text
path/to/sampleA.bakta.json
path/to/sampleB.bakta.json
```

Or explicit sample/path pairs:

```text
sampleA path/to/sampleA.bakta.json
sampleB path/to/sampleB.bakta.json
```

Relative paths are resolved relative to the list file.

## Checksums

Each archive sample stores:

```text
original_json_canonical_sha256
reduced_json_canonical_sha256
```

Canonical hashes ignore JSON object key order and whitespace but preserve array order and values.

Reduced JSON files include a top-level `_bakpack` metadata object containing `original_json_canonical_sha256`. `restore` removes this metadata before reconstructing the original JSON and verifies the reconstructed canonical SHA-256 automatically.

Archive extraction always verifies reduced JSON canonical SHA-256. Reconstructed original JSON extraction verifies original canonical SHA-256.

## Library Usage

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

## Development

Run tests:

```bash
go test ./...
```

Use a local cache inside the repo if your environment blocks the default Go cache:

```bash
GOCACHE="$PWD/.cache/gocache" go test ./... -count=1
```

Build locally:

```bash
./build.sh
```

Build all release targets without packaging:

```bash
./build.sh --all
```

Build release artifacts:

```bash
./build.sh --release --version v0.1.0
```

Release artifacts go to `dist/` and include darwin/linux/windows builds for amd64 and arm64 plus a SHA-256 checksum file.

GitHub Actions:

- `.github/workflows/test.yml` runs `go test ./...` on pushes to `main` and pull requests.
- `.github/workflows/release.yml` builds and publishes release artifacts when a tag matching `v*.*.*` is pushed.

The build script injects the version with:

```text
-X github.com/martinghunt/bakpack/internal/buildinfo.Version=<version>
```

Check it with:

```bash
bakpack --version
```
