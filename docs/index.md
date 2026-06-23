# bakpack

`bakpack` compresses Bakta JSON annotation files while keeping them
reconstructable and checkable against the original JSON content.

It removes sequence-derived fields from Bakta JSON, stores the remaining
annotation data in a chunked `.bakpack` archive, and verifies extracted or
reconstructed annotations with canonical SHA-256 checksums.

Source code: [github.com/martinghunt/bakpack](https://github.com/martinghunt/bakpack)

## What it does

Make one reduced JSON file:

```text
$ bakpack reduce sample.bakta.json sample.fa -o sample.reduced.bakta.json
original_json_canonical_sha256  ...
reduced_json_canonical_sha256   ...
```

Reconstruct original JSON content:

```text
$ bakpack restore sample.reduced.bakta.json sample.fa -o sample.bakta.json
original_json_canonical_sha256  ...
```

Build a compressed archive:

```text
$ bakpack build --annotations annotations.tar.xz --genomes genomes.tar.xz -o annotations.bakpack
```

Extract one reconstructed annotation:

```text
$ bakpack extract annotations.bakpack SAMN1 --genomes genomes.tar.xz --original -o out
```

Extract several reduced annotations efficiently:

```text
$ bakpack extract annotations.bakpack SAMN1 SAMN2 SAMN3 --reduced -o out
```

## Quick start

1. Install `bakpack`.
2. Build an archive from matching Bakta JSON and genome FASTA sources:

   ```
   bakpack build \
     --annotations annotations.tar.xz \
     --genomes genomes.tar.xz \
     --output annotations.bakpack
   ```

3. Extract an annotation:

   ```
   bakpack extract annotations.bakpack SAMPLE \
     --genomes genomes.tar.xz \
     --original \
     --output-dir out
   ```

4. Use a combined manifest when filenames do not encode sample IDs:

   ```
   bakpack build --manifest samples.tsv --output annotations.bakpack
   ```

5. Use an HTTP(S) archive URL when the server supports byte-range requests:

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
