# bakpack

`bakpack` is a Go command line tool and library for compressing Bakta JSON annotation files while keeping them reconstructable and checkable against the original JSON content.

This repository was developed with substantial coding assistance from [OpenAI Codex](https://openai.com/codex), which helped with implementation, refactoring, tests, documentation, and benchmarking under human direction and review.

Documentation: [bakpack.readthedocs.io](https://bakpack.readthedocs.io/en/)

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

Archive creation uses the command line `xz` program:

```text
xz -9e -T1 -c
```

Put `xz` in `PATH`. AGC genome input requires `agc` in `PATH`.

## Quick Start

Make one reduced JSON file:

```bash
bakpack reduce sample.bakta.json sample.fa -o sample.reduced.bakta.json
```

Reconstruct original JSON content:

```bash
bakpack restore sample.reduced.bakta.json sample.fa -o sample.bakta.json
```

Render a Bakta-style GFF3 file from original or reduced JSON:

```bash
bakpack gff3 sample.bakta.json sample.fa -o sample.gff3
```

Omit the terminal FASTA section when only annotation rows are needed:

```bash
bakpack gff3 sample.bakta.json sample.fa -o sample.gff3 --annotation-only
```

Build a compressed archive:

```bash
bakpack build \
  --annotations annotations.tar.xz \
  --genomes genomes.tar.xz \
  --output annotations.bakpack
```

Build from a combined manifest:

```bash
bakpack build \
  --manifest samples.tsv \
  --output annotations.bakpack
```

Extract one reconstructed annotation and its genome FASTA:

```bash
bakpack extract annotations.bakpack SAMN1 \
  --genomes genomes.tar.xz \
  --original \
  --gff3 \
  --gff3-annotation-only \
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
