# GFF3 reference files

`synthetic.bakta.json` and `synthetic.fa` are a small Bakta 1.12 annotation
made by `make_synthetic.py` to reach every branch of Bakta's GFF3 writer:
every feature type, pseudogenes, truncated features, CDS exceptions, signal
peptides, a feature wrapping round a circular contig, every ncRNA and
regulatory class, and characters that need escaping.

The two `.gff3` files were written from that JSON by Bakta v1.12.2's own GFF3
writer (`bakta/io/gff.py`), using `bakta_gff_ref.py`:

```
python3 make_synthetic.py .
python3 bakta_gff_ref.py BAKTA_CHECKOUT synthetic.bakta.json synthetic.bakta-1.12.2.gff3
python3 bakta_gff_ref.py BAKTA_CHECKOUT synthetic.bakta.json synthetic.bakta-1.12.2.compliant.gff3 --compliant
```

`synthetic.noncompliant.bakta.json` is the same annotation as Bakta writes
without `--compliant`, with product names to revise, a contig under 200 bp,
and a contig ID and locus tag prefix that INSDC rejects. Its expected compliant
GFF3 applies Bakta's own annotation-time compliant changes first:

```
python3 bakta_gff_ref.py BAKTA_CHECKOUT synthetic.noncompliant.bakta.json synthetic.noncompliant.bakta-1.12.2.compliant.gff3 --compliant --annotate-compliant
```

`synthetic.v1.9.bakta.json` is the annotation in the Bakta < 1.10 JSON layout.
Its GFF3 files are written by Bakta 1.12.2 in the same way, as `bakta_io` does
for old JSON.

`BAKTA_CHECKOUT` is a checkout of the Bakta repository at v1.12.2. Biopython
and xopen must be installed. Bakta's GFF3 output is the same for every release
from 1.10.0 to 1.12.2.
