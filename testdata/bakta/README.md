# Bakta reference files

`make_synthetic.py` writes small Bakta annotations that reach every branch of
Bakta's GFF3, GenBank and EMBL output: every feature type, pseudogenes,
truncated features of each kind, CDS exceptions, signal peptides, features
wrapping round circular sequences on each strand, plasmid and chromosome
sequences, database hits behind CDS inferences, every ncRNA and regulatory
class, and characters that need escaping:

- `synthetic.bakta.json` and `synthetic.fa`: a Bakta 1.12 annotation.
- `synthetic.noncompliant.bakta.json` and `synthetic.noncompliant.fa`: the
  same as Bakta writes without `--compliant`, with product names to revise, a
  contig under 200 bp, and a contig ID and locus tag prefix that INSDC
  rejects.
- `synthetic.v1.9.bakta.json`: the annotation in the Bakta < 1.10 JSON layout
  (uses `synthetic.fa`).

The `.gff3`, `.gbff` and `.embl` files were written from them by Bakta
v1.12.2's own writers (`bakta/io/gff.py` and `bakta/io/insdc.py`), as its
`bakta_io` does, using `bakta_gff_ref.py` and `bakta_insdc_ref.py`. These
scripts import Bakta from a checkout; they contain none of its code. Files
named `.compliant` are written in `--compliant` mode. For the non-compliant
annotation, `--annotate-compliant` first applies the changes Bakta
`--compliant` makes while annotating.

```
python3 make_synthetic.py .
for json in synthetic synthetic.v1.9; do
  python3 bakta_gff_ref.py BAKTA_CHECKOUT $json.bakta.json $json.bakta-1.12.2.gff3
  python3 bakta_gff_ref.py BAKTA_CHECKOUT $json.bakta.json $json.bakta-1.12.2.compliant.gff3 --compliant
  python3 bakta_insdc_ref.py BAKTA_CHECKOUT $json.bakta.json $json.bakta-1.12.2
  python3 bakta_insdc_ref.py BAKTA_CHECKOUT $json.bakta.json $json.bakta-1.12.2.compliant --compliant
done
python3 bakta_gff_ref.py BAKTA_CHECKOUT synthetic.noncompliant.bakta.json synthetic.noncompliant.bakta-1.12.2.compliant.gff3 --compliant --annotate-compliant
python3 bakta_insdc_ref.py BAKTA_CHECKOUT synthetic.noncompliant.bakta.json synthetic.noncompliant.bakta-1.12.2.compliant --compliant --annotate-compliant
```

`BAKTA_CHECKOUT` is a checkout of the Bakta repository at v1.12.2. Biopython
and xopen must be installed. Bakta's GFF3 output is the same for every release
from 1.10.0 to 1.12.2; its GenBank and EMBL output changed in small ways
within that range, and bakpack follows 1.12.
