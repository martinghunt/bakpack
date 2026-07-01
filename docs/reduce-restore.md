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
