# Bakta GenBank and EMBL output

This describes the GenBank and EMBL that current Bakta (1.12) writes for an
annotation JSON, in its default mode and in `--compliant` mode, as observed
from its output. It is a description of the output only. Bakta's files are
laid out by Biopython's GenBank and EMBL writers, which faqt's `insdc` package
reproduces byte for byte, so this document describes the records: the
sequence, header annotations and features, in the terms of faqt's
`insdc.Record`. The tests compare bakpack with files written by Bakta itself
(`testdata/bakta`).

GenBank and EMBL are written from the same records. They apply to JSON from
any Bakta version; JSON from before Bakta 1.10 uses an older layout (features
name their sequence with `contig`, sequences hold their nucleotides in
`sequence` rather than `nt`) and is written as current Bakta writes it.

Value formatting, "truthy", and what it means for a feature to "have" a key
are as in [the GFF3 format description](bakta-gff3-format.md). Qualifier
values that are JSON integers are written as integers (unquoted, `insdc.Int`);
a `null` value given to a qualifier is written as the text `None`; JSON floats
are text in Python float formatting.

## Records

One record per entry of `sequences`, in order. In compliant mode, sequences
shorter than 200 are left out, as for GFF3, along with their features.

- `ID` and `Name`: the sequence `id`.
- `Seq`: the sequence's nucleotides.
- `Description`: built from these parts, then with one leading space removed
  if it starts with a space:
  - start with `genome.taxon` if it is truthy, otherwise empty;
  - sequence `type` `plasmid`: append ` plasmid ` and the sequence's `name`
    (`unnamed` if the key is missing; a present null is written `None`), then
    `, complete sequence` if `complete` is truthy, otherwise
    `, whole genome shotgun sequence`;
  - `chromosome`: if `complete` is truthy, append ` chromosome, complete
    genome`; otherwise ` chromosome ID, whole genome shotgun sequence`;
  - any other type: append ` ID, whole genome shotgun sequence`.

Annotations:

- `MoleculeType`: `DNA`.
- `Source`: `genome.taxon` formatted as a value if the key is present (so null
  is `None`), otherwise the empty string. Always set.
- `Organism`: `genome.taxon`, only if it is truthy (otherwise unset).
- `Date`: the annotation date (the JSON `run.end`, or the date bakpack is given
  for JSON without `run`) as `DD-MON-YYYY` with an upper case English month
  abbreviation, e.g. `01-JAN-2026`.
- `Topology`: the sequence `topology`.
- `DataFileDivision`: `HGT` if the sequence `type` is `contig`, otherwise
  `BCT`. (GenBank writes `HGT` as `UNK`, and EMBL as `UNC`.)
- `Comment`: the lines below, where counts are of this sequence's features
  (after compliant mode's changes), each right-aligned in five characters with
  `,` as the thousands separator (Python `{:5,}`), and each label left-aligned
  and padded to 30 characters:
  1. `Annotated with Bakta`
  2. `Software: vVERSION` with `version.bakta`
  3. `Database: vMAJOR.MINOR, TYPE` from `version.db` as in the GFF3 header
  4. `DOI: 10.1099/mgen.0.000685`
  5. `URL: github.com/oschwengers/bakta`
  6. an empty line
  7. `##Genome Annotation Summary:##`
  8. `Annotation Date` and the annotation date as `MM/DD/YYYY, HH:MM:SS`
  9. `CDSs`: `cds` and `sorf` features
  10. `tRNAs`, `tmRNAs`, `rRNAs`, `ncRNAs`: features of those types
  11. `regulatory ncRNAs`: `ncRNA-region` features
  12. `CRISPR Arrays`: `crispr` features
  13. `oriCs/oriVs`: `oriC` and `oriV` features
  14. `oriTs`, `gaps`: `oriT` and `gap` features
  15. `pseudogenes`: `cds` features that have `pseudogene`

  Lines 8 to 15 are each `LABEL :: COUNT`, e.g.
  `CDSs                           ::     7` (label padded to 30, then ` :: `).

No other annotations (accessions, keywords, taxonomy, references) and no
DBLINK entries are set.

## Features

Each record's features are, in order:

1. A `source` feature covering the whole sequence (start 0, end the sequence
   length, strand 1) with qualifiers, in order: `mol_type` `genomic DNA`;
   `organism` = `genome.taxon` if truthy; `strain` = `genome.strain` if truthy;
   then for a `plasmid` sequence `plasmid` = its `name` if truthy, otherwise
   `unnamed`; for a `chromosome` sequence `chromosome` = its `name`, only if
   truthy.
2. For each of the sequence's features, in JSON order (features on sequence
   IDs not in `sequences` are not written): a `gene` feature if
   the feature's `locus` is truthy, then the feature itself, then a signal
   peptide feature if it has one.

Locations are 0-based with an exclusive end, as in `insdc.Location`.

### Qualifiers common to all features

Qualifiers are ordered by when they are first set; setting one again replaces
its value in place. A qualifier whose value is a list is written once per item
(none for an empty list). A flag qualifier (`insdc.Flag`, e.g. `/pseudo`) has
no value.

Every feature starts with these, in this order:

- `note`: an empty list.
- `db_xref`: the feature's `db_xrefs`, if it has that key. In compliant mode,
  the INSDC-valid Dbxrefs instead (as defined for GFF3), and `note` is set to
  the invalid ones.
- `product`: the feature's `product`, if it has that key (in compliant mode,
  revised for `cds` and `sorf` as for GFF3).
- `locus_tag`: the feature's `locus`, if it has that key.

Then the type-specific qualifiers below, then the location's qualifiers, then
`gene` (see "Gene features").

### By type

- **gap**: feature type `gap`; `estimated_length` = `length`.
- **oriC, oriV**: type `rep_origin`; `inference` `similar to DNA sequence`;
  `note` is set to the product (a single value, replacing any notes); `product`
  is removed.
- **oriT**: type `oriT`; `inference` `similar to DNA sequence`; the product is
  appended to `note`; `product` is removed.
- **cds, sorf**: type `CDS`.
  - If the feature has `pseudogene`: `pseudogene` = `unprocessed` if
    `pseudogene.paralog` is truthy, otherwise `unitary`; and
    `pseudogene.description` is appended to `note`. Otherwise: `protein_id` =
    `gnl|Bakta|` + locus, and `translation` = the feature's `aa`.
  - `codon_start` = 1 (integer); `transl_table` = `genome.translation_table`.
  - `inference`, a list of one or two values. The first: for `cds`,
    `EXISTENCE:non-experimental evidence, no additional details recorded` if
    the feature's `source` is `user-provided`, otherwise
    `ab initio prediction:Prodigal:2.6`; for `sorf`,
    `ab initio prediction:Bakta:MAJOR.MINOR` from the first two `.`-separated
    parts of `version.bakta`. The second, the first that applies of:
    `similar to AA sequence:RefSeq:ID` with `ups.ncbi_nrp_id`;
    `similar to AA sequence:UniRef:ID` with `ips.uniref100_id`;
    `similar to AA sequence:UniParc:ID` with `ups.uniparc_id`;
    `similar to AA sequence:UniRef:ID` with `psc.uniref90_id`, only if
    `psc.valid` is truthy; `similar to AA sequence:UniRef:ID` with
    `pscc.uniref50_id`. (A missing object or key does not apply.)
  - Compliant mode only: the GFF3 "EC numbers" step on `note`, setting
    `EC_number` (note the capitals) instead of `ec_number`.
  - If the feature has an `exception` object: for type `selenocysteine`,
    `transl_except` = `(pos:START..STOP,aa:AA)`, with `START..STOP` wrapped as
    `complement(START..STOP)` when the feature strand is `-`, and
    `codon on position CODON_POSITION is a selenocysteine codon` appended to
    `note`; for type `ribosomal_slippage`, a `ribosomal_slippage` flag.
  - If the feature has a `signal-peptide` object, a `sig_peptide` feature
    follows the CDS (after any gene feature and the CDS): location from the
    peptide's `start` - 1 to its `stop`, strand 1 if the feature strand is `+`,
    -1 if `-`, otherwise 0; qualifiers `locus_tag` = locus and `inference`
    `ab initio prediction:DeepSig:1.2`.
- **tRNA**: type `tRNA`. If the feature has both `amino_acid` and
  `anti_codon` keys: with an `anti_codon_pos` key (a two-item list),
  `anticodon` = `(pos:P1..P2,aa:AMINO_ACID,seq:ANTI_CODON)`; without it,
  `tRNA-AMINO_ACID (ANTI_CODON)` is appended to `note`. Then `inference`
  `profile:tRNAscan:2.0`; then `pseudogene` `unknown` if the feature has
  `pseudogene`.
- **tmRNA**: type `tmRNA`; `inference` `profile:aragorn:1.2`; `tag_peptide` =
  `TAG_START..TAG_STOP` from the `tag` object, if present.
- **rRNA**: type `rRNA`; the GFF3 "Rfam inference" step, using the feature's
  `db_xrefs` (not the compliant subset).
- **ncRNA**: type `ncRNA`; Rfam inference; `ncRNA_class` as for GFF3.
- **ncRNA-region**: type `regulatory`; Rfam inference; `regulatory_class` as
  for GFF3; the product is appended to `note`; `product` is removed.
- **crispr**: type `repeat_region`; `rpt_family` `CRISPR`, `rpt_type`
  `direct`, `rpt_unit_seq` = `repeat_consensus`, `inference`
  `COORDINATES:alignment:pilercr:1.02`; the product is appended to `note`;
  `product` is removed.

### Location

The strand is 1 for `+`, -1 for `-`, 0 for `?`, and none (0) otherwise. The
location runs from `start` - 1 to `stop`.

- A feature that has `edge` wraps round the origin: a `join` of two parts,
  from `start` - 1 to the sequence length and from 0 to `stop`, both with the
  feature's strand. For strand `-` the parts are in the opposite order (0 to
  `stop` first). Nothing else below applies to it.
- Otherwise, if the feature has `truncated`: first, unless it has
  `pseudogene`, add a `pseudo` flag. Then by the `truncated` value:
  - `5-prime`: append `(5' truncated)` to `note`; for strand `+` the start is
    a "before" position (`<`), otherwise the end is an "after" position (`>`).
  - `3-prime`: append `(3' truncated)`; for strand `+` the end is "after",
    otherwise the start is "before".
  - `both`: append `(partial)`; the start is "before" and the end "after".

### Gene features

If the feature's `locus` is truthy, a `gene` feature with the same location
precedes it, with qualifiers: `locus_tag`; then, if the feature's `gene` is
truthy, `gene` (in compliant mode only if it consists entirely of three lower
case letters, an upper case letter and an optional digit; otherwise it is also
removed from the feature); then `pseudogene` if the feature has `pseudogene`
(for `cds`, `unprocessed` or `unitary` as above; for other types `unknown`),
otherwise a `pseudo` flag if the feature has `truncated`.

When the gene is kept, `gene` is also set on the feature itself, after all its
other qualifiers.

## Compliant mode's annotation changes and warnings

As for GFF3 (see [the GFF3 description](bakta-gff3-format.md)): CDS and sORF
products are revised, sequences shorter than 200 are left out with their
features, and the same warnings are given.
