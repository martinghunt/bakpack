# bakpack

`bakpack` compresses Bakta JSON annotation files while keeping them
reconstructable and checkable against the original JSON content.

It supports two main things:

1. Reduce the size of a single Bakta JSON file by removing information that can
   be reconstructed from the genome sequence.
2. Pack reduced JSON files into a single archive, with easy retrieval of any
   sample as the original Bakta JSON. In testing, these archives are typically
   about 10% of the size of an xz-compressed tar archive of the original JSON
   files.

Extracted and reconstructed JSON files are checked against a checksum of the
original Bakta JSON. The reduction/compression is lossless; the only expected
differences are JSON object key order and whitespace.

Source code: [github.com/martinghunt/bakpack](https://github.com/martinghunt/bakpack)

## Quick start

Install `bakpack`. The easiest method is to download the latest build from the
[latest release](https://github.com/martinghunt/bakpack/releases/latest).

Make a reduced JSON file:

```text
bakpack reduce sample.bakta.json sample.fa -o sample.reduced.bakta.json
```

Restore the original JSON content:

```text
bakpack restore sample.reduced.bakta.json sample.fa -o sample.bakta.json
```

Render GFF3 from original or reduced JSON:

```text
bakpack gff3 sample.bakta.json sample.fa -o sample.gff3
```

Omit the terminal FASTA section when only annotation rows are needed:

```text
bakpack gff3 sample.bakta.json sample.fa -o sample.gff3 --annotation-only
```

Build an archive from matching Bakta JSON and genome FASTA sources:

```
bakpack build \
  --annotations annotations.tar.xz \
  --genomes genomes.tar.xz \
  --output annotations.bakpack
```

Extract an annotation:

```
bakpack extract annotations.bakpack SAMPLE \
  --genomes genomes.tar.xz \
  --original \
  --gff3-annotation-only \
  --output-dir out
```

Use an HTTP(S) archive URL when the server supports byte-range requests:

```
bakpack extract https://example.org/annotations.bakpack SAMPLE \
  --genomes genomes.tar.xz \
  --original \
  --output-dir out
```

For more detail, see:

```{toctree}
:maxdepth: 2
:caption: Contents

install
reduce-restore
build
extract
input-sources
checksums
archive-format
library
release
```
