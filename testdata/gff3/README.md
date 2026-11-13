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

`BAKTA_CHECKOUT` is a checkout of the Bakta repository at v1.12.2. Biopython
and xopen must be installed. Bakta's GFF3 output is the same for every release
from 1.10.0 to 1.12.2.
