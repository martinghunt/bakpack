# Reduce and restore

`bakpack reduce` writes a reduced Bakta JSON file by removing fields that can be
reconstructed from the matching genome FASTA.

```
bakpack reduce BAKTA_JSON GENOME_FASTA -o REDUCED_JSON
```

`bakpack restore` recreates original Bakta JSON content from reduced JSON and
the matching genome FASTA.

```
bakpack restore REDUCED_JSON GENOME_FASTA -o BAKTA_JSON
```

`bakpack gff3` renders a Bakta-style GFF3 file from original or reduced JSON
and the matching genome FASTA.

```
bakpack gff3 BAKTA_JSON GENOME_FASTA -o SAMPLE.gff3
```

Use `--annotation-only` to omit the terminal `##FASTA` section:

```
bakpack gff3 BAKTA_JSON GENOME_FASTA -o SAMPLE.gff3 --annotation-only
```

The GFF3 matches what the Bakta version recorded in the JSON writes. Bakta
changed its GFF3 output in v1.10.0, so JSON from Bakta 1.10 or later is written
in the newer style and older JSON in the older one. If the JSON has no Bakta
version, its layout decides (Bakta 1.10 changed the JSON layout too).

Use `--compliant` to write GFF3 as Bakta's `--compliant` (Genbank/ENA/DDBJ)
mode does. Bakta's JSON does not record whether `--compliant` was used, and
`bakta_io` cannot write compliant files from JSON, so this is the way to get
compliant GFF3 from a bakpack archive or reduced JSON. It is available for JSON
from Bakta 1.10 or later:

```
bakpack gff3 BAKTA_JSON GENOME_FASTA -o SAMPLE.gff3 --compliant
```

Compliant GFF3 has no `##FASTA` section, as with Bakta.

Bakta's `--compliant` mode also changes the annotation itself, before any file
is written. bakpack applies those changes too, so the GFF3 is compliant even
when Bakta was run without `--compliant`:

- CDS and sORF product names are revised as Bakta does: "uncharacterized" and
  "uncharacterised" become "putative", and unbalanced brackets are removed.
- Contigs shorter than 200 bp are left out, with their features.
- Contig IDs and locus tag prefixes that INSDC would reject are reported as
  warnings. Bakta refuses to run with them; bakpack cannot rename them.

These changes leave JSON from a `--compliant` run unchanged, so its output
matches Bakta's exactly. For JSON from a run without `--compliant`, two things
can still differ from what a `--compliant` run would have produced: if short
contigs were left out, the remaining contigs and locus tags keep their
original numbers, and in the rare case that removing unbalanced brackets
reveals a gene symbol (e.g. `(abcD`), Bakta would also add it as a gene.

## Many samples at once

Run `reduce`, `restore` or `gff3` without positional arguments to process every
sample in an annotation source and a genome source. The sources are the same as
for `bakpack build` (see [input sources](input-sources.md)): directories, file
lists, a `--manifest`, or tar files in any supported compression.

```
bakpack reduce --annotations bakta_jsons.tar --genomes genomes.tar -o reduced.tar.xz
bakpack restore --annotations reduced.tar.xz --genomes genomes.tar -o restored/
bakpack gff3 --annotations reduced.tar.xz --genomes genomes.tar -o gff3.tar.gz
```

`--output` is a tar file when it ends in `.tar`, `.tar.gz`, `.tgz`, `.tar.bz2`,
`.tbz2`, `.tar.xz`, `.txz`, `.tar.zst` or `.tzst`, and otherwise a directory,
created if needed. Each sample produces one file, named as `bakpack extract`
names them:

| Command | Output file |
|---|---|
| `reduce` | `SAMPLE.reduced.bakta.json` |
| `restore` | `SAMPLE.bakta.json` |
| `gff3` | `SAMPLE.gff3` |

Reduced JSON named `SAMPLE.reduced.bakta.json` (or `SAMPLE.reduced.json`)
pairs with the genome `SAMPLE.fa`, so the output of `reduce` can be passed
straight back to `restore` or `gff3`.

Samples are written in genome source order, or in the order given by `--order`.
Each tar source is read once. The run stops at the first sample that fails, and
a tar output is then not created. `.tar.xz` output is compressed with `xz` from
`PATH`; use `--xz-threads` to give it more threads.

A reduced-JSON tar suits samples that are processed one at a time or whose
genomes are unrelated. A `.bakpack` archive (`bakpack build`) is usually much
smaller when the genomes are related, because it compresses across samples.

## Removed fields

The current reducer can remove:

- `sequences[].sequence`
- feature `nt`
- feature `aa`
- `stats.no_sequences`
- `stats.size`
- `stats.n_ratio`
- `stats.n50`
- `sequences[].length`
- protein feature `aa_hexdigest`
- CDS feature `start_type`
- `hypothetical` when `product` is `hypothetical protein`
- gap feature `length`

Values are removed only when `bakpack` can derive the same value from the
matching genome sequence and the remaining annotation content.

## Correctness

Reduced JSON embeds the original canonical JSON checksum in a top-level
`_bakpack` metadata object. Restore removes this object, reconstructs missing
values, and verifies the reconstructed canonical SHA-256.

Object key order and whitespace are not preserved. JSON array order and values
are preserved.
