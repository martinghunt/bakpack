# bakpack

`bakpack` is a Go library and command line tool for compressing Bakta JSON annotations while keeping them recoverable and checkable against the original JSON content.

The current implementation is the first clean Go version. It prioritizes correctness, source handling, and a stable API. The archive payload is chunked reduced JSON compressed with xz; the columnar/specialized codecs from the Python prototype can replace that payload later without changing the user-facing workflows.

Archive creation uses the command-line `xz` program by default:

```text
xz -9e -T1 -c
```

This matches the high-compression setting used in the Python prototype. Decompression is handled in Go. If a pure-Go build path is required, pass `--go-xz`; this uses `github.com/ulikunitz/xz`, which is portable but is expected to compress worse than the command-line `xz` implementation.

## Commands

Reduce one Bakta JSON file using its matching genome FASTA:

```bash
bakpack reduce sample.bakta.json sample.fa -o sample.reduced.bakta.json
```

Restore original JSON content from reduced JSON and genome FASTA:

```bash
bakpack restore sample.reduced.bakta.json sample.fa -o sample.bakta.json
```

Build an archive:

```bash
bakpack build \
  --annotations annotations.tar.xz \
  --genomes genomes.tar.xz \
  --output annotations.bakpack
```

Set xz thread count if needed:

```bash
bakpack build \
  --annotations annotations \
  --genomes genomes \
  --xz-threads 4 \
  --output annotations.bakpack
```

Override the xz command and full argument list if needed:

```bash
bakpack build \
  --annotations annotations \
  --genomes genomes \
  --xz-command /path/to/xz \
  --xz-arg -9e \
  --xz-arg -T4 \
  --xz-arg -c \
  --output annotations.bakpack
```

`--xz-threads` controls the `-T` value when using the default xz arguments. Supplying any `--xz-arg` values replaces the default xz argument list completely.

Extract reduced JSON for one or more samples:

```bash
bakpack extract annotations.bakpack SAMN1 SAMN2 --output-dir out
```

Extract reconstructed original JSON and the matching genome FASTA:

```bash
bakpack extract annotations.bakpack SAMN1 \
  --genomes genomes.tar.xz \
  --original \
  --genome \
  --output-dir out
```

Print the archive index:

```bash
bakpack index annotations.bakpack
```

## Inputs

Annotation sources support:

- directory
- file list
- `.tar.xz`

Genome sources support:

- directory
- file list
- `.tar.xz`
- `.agc`

Source format can be inferred from the path or set explicitly:

```bash
--annotations-format dir|list|tar.xz|auto
--genomes-format dir|list|tar.xz|agc|auto
```

For directories, sample IDs are inferred from filenames:

```text
sample.bakta.json -> sample
sample.json       -> sample
sample.fa         -> sample
sample.fasta      -> sample
sample.fna        -> sample
```

File lists can contain either one path per line or tab/space-separated sample/path pairs:

```text
sampleA path/to/sampleA.bakta.json
sampleB path/to/sampleB.bakta.json
```

Relative paths in file lists are resolved relative to the list file.

## Sample Order

When building an archive, default order is the genome source order. This matters for compression because genomes ordered by similarity usually give better annotation chunks too.

You can override order explicitly:

```bash
bakpack build \
  --annotations annotations \
  --genomes genomes \
  --order sample_order.txt \
  --output annotations.bakpack
```

The order file contains one sample ID per line. It must contain every annotation sample exactly once.

## Checksums

Each archive sample stores four SHA-256 values:

```text
original_json_bytes_sha256
original_json_canonical_sha256
reduced_json_bytes_sha256
reduced_json_canonical_sha256
```

The byte hashes are exact file-byte diagnostics. The canonical hashes are the correctness checks that matter: JSON object key order and whitespace do not matter, but array order and values do.

Extraction always verifies reduced JSON byte and canonical SHA-256. When reconstructing original JSON, extraction verifies the reconstructed original JSON canonical SHA-256.

## Library

The CLI is a thin wrapper around the `github.com/martinghunt/bakpack` package.

Core API entry points:

```go
result, err := bakpack.ReduceBaktaJSON(originalJSON, genome)
restored, err := bakpack.RestoreBaktaJSON(reducedJSON, genome)
err := bakpack.BuildArchive(ctx, bakpack.BuildOptions{...})
err := bakpack.ExtractArchive(ctx, bakpack.ExtractOptions{...})
```

Input sources use one interface:

```go
type FileSource interface {
    Records(context.Context) ([]FileRecord, error)
    Get(context.Context, sample string) (FileRecord, error)
    Order(context.Context) ([]string, error)
}
```

`OpenSource(path, format, role)` returns directory, list, tar.xz, or AGC-backed implementations.

AGC support shells out to `agc` in `PATH` using `listset` for sample order and `getset` for FASTA retrieval.

## Tests

The tests use toy Bakta JSON and small genomes inside real `.tar.xz` archives. They cover:

- reducing and restoring JSON with canonical checksum validation
- building from tar.xz annotation/genome archives
- defaulting archive order to genome archive order
- building from annotation directories and genome file lists
- extracting multiple samples from an archive
- extracting reconstructed original JSON and genome FASTA
- Cobra CLI workflows
