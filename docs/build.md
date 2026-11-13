# Build archives

Build a `.bakpack` archive from Bakta annotation JSON and matching genome FASTA
sources:

```
bakpack build \
  --annotations ANNOTATION_SOURCE \
  --genomes GENOME_SOURCE \
  --output annotations.bakpack
```

Common options:

```text
--manifest           tab-delimited manifest with sample, annotation, and genome paths
--annotations-format auto|dir|list|manifest|tar|tar.xz
--genomes-format     auto|dir|list|manifest|tar|tar.xz|agc
--chunk-size         samples per compressed chunk, default 25
--order              file of sample IDs defining archive order
--xz-threads         threads passed as xz -T, default 1
```

## Archive order

Default sample order is the genome source order. This can improve compression
when the genome source order groups similar genomes, because related annotations
tend to share more structure and repeated values.

When `--manifest` is used, the manifest row order is the default archive order.

Use `--order` to override the source order. The order file has one sample ID per
line. The whole non-comment line is used as the sample ID:

```text
# comments are allowed
sampleB
sampleA
sampleC
```

Blank lines and lines starting with `#` are ignored.

## Reading tar sources

A tar source can only be read from start to end, so `build` reads each `.tar`
or `.tar.xz` source once:

- When both sources are tars, `build` walks them together while their sample
  IDs agree. If the orders diverge, it writes the rest of the annotations to
  temporary files beside the output and pairs them with the rest of the genome
  tar as it streams.
- When only the annotations are a tar, they are written to temporary files
  first, then paired with the genome source.
- When the genomes are a tar and `--order` is given, the genomes needed are
  written to temporary files first, then read back in `--order` order.

Temporary files are gzip-compressed unless `--annotation-spool-compression none`
is given, and are deleted as each sample is paired.

## Examples

Build from tar archives:

```
bakpack build \
  --annotations annotations.tar.xz \
  --genomes genomes.tar.xz \
  --output annotations.bakpack
```

Build from directories:

```
bakpack build \
  --annotations annotations/ \
  --genomes genomes/ \
  --output annotations.bakpack
```

Build from a combined manifest:

```
bakpack build \
  --manifest samples.tsv \
  --output annotations.bakpack
```

Build with AGC genome input:

```
bakpack build \
  --annotations annotations.tar.xz \
  --genomes genomes.agc \
  --genomes-format agc \
  --output annotations.bakpack
```
