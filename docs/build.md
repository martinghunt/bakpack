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
--manifest           combined manifest with sample, annotation, and genome paths
--annotations-format auto|dir|list|manifest|tar.xz
--genomes-format     auto|dir|list|manifest|tar.xz|agc
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
line:

```text
# comments are allowed
sampleB
sampleA
sampleC
```

Blank lines and lines starting with `#` are ignored.

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
