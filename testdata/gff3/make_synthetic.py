"""Write a small Bakta >= 1.10 JSON and FASTA covering every GFF3 code path.

Usage: python3 make_synthetic.py OUT_DIR
"""
import hashlib
import json
import random
import sys
from pathlib import Path

out = Path(sys.argv[1])
random.seed(7)
contig1 = ''.join(random.choice('ACGT') for _ in range(3000))
contig2 = ''.join(random.choice('ACGT') for _ in range(1200))


def feat(**kw):
    return kw


features = [
    # CDS: plain, with gene, EC and INSDC-valid and invalid Dbxrefs, signal peptide
    feat(type='cds', sequence='contig_1', start=1, stop=300, strand='+', frame=1, gene='abcD', product='Widget; protein=A, & B %',
         db_xrefs=['EC:1.2.3.4', 'GO:0005840', 'UniRef:UniRef90_X', 'Pfam:PF00001', 'UniProtKB/TrEMBL:Q1', 'EC:2.7.7.7', 'SO:0001217'],
         locus='SYN_00001', id='SYN1', **{'signal-peptide': {'start': 1, 'stop': 60, 'score': 0.875, 'aa': 'MAAA'}}),
    # CDS: unitary pseudogene, no gene
    feat(type='cds', sequence='contig_1', start=310, stop=600, strand='-', frame=2, gene=None, product='hypothetical protein',
         db_xrefs=['SO:0001217'], locus='SYN_00002', id='SYN2', pseudogene={'paralog': False, 'causes': ['insertion']}),
    # CDS: unprocessed (paralog) pseudogene, with gene
    feat(type='cds', sequence='contig_1', start=610, stop=900, strand='+', frame=1, gene='xyzA', product='Paralog protein',
         db_xrefs=[], locus='SYN_00003', id='SYN3', pseudogene={'paralog': True}),
    # CDS: truncated, user-provided, selenocysteine exception
    feat(type='cds', sequence='contig_1', start=910, stop=1200, strand='-', frame=3, gene='fdhF', product='Formate dehydrogenase',
         db_xrefs=['EC:1.17.1.9'], locus='SYN_00004', id='SYN4', truncated='5-prime', source='user-provided',
         exception={'type': 'selenocysteine', 'start': 1000, 'stop': 1002, 'aa': 'Sec', 'codon_position': 31}),
    # CDS: ribosomal slippage exception
    feat(type='cds', sequence='contig_1', start=1210, stop=1500, strand='+', frame=1, gene='prfB', product='Peptide chain release factor 2',
         db_xrefs=['UniRef:UniRef50_P'], locus='SYN_00005', id='SYN5', exception={'type': 'ribosomal_slippage', 'start': 1270, 'stop': 1272}),
    # sORF with gene, EC and signal peptide
    feat(type='sorf', sequence='contig_1', start=1510, stop=1560, strand='+', frame=1, gene='smlA', product='Small protein',
         db_xrefs=['EC:3.1.1.1', 'UniRef:UniRef90_S'], locus='SYN_00006', id='SYN6',
         **{'signal-peptide': {'start': 1510, 'stop': 1530, 'score': 0.5, 'aa': 'MK'}}),
    # sORF without gene
    feat(type='sorf', sequence='contig_1', start=1570, stop=1620, strand='-', frame=2, gene=None, product='hypothetical protein',
         db_xrefs=[], locus='SYN_00007', id='SYN7'),
    # tRNA: plain, pseudogene, truncated
    feat(type='tRNA', sequence='contig_1', start=1630, stop=1702, strand='+', gene='trnA', product='tRNA-Ala', amino_acid='Ala',
         anti_codon='tgc', score=60.5, db_xrefs=['SO:0000254'], locus='SYN_00008', id='SYN8'),
    feat(type='tRNA', sequence='contig_1', start=1710, stop=1782, strand='-', gene='trnR', product='tRNA-Arg', amino_acid='Arg',
         anti_codon='ccg', score=30.1, db_xrefs=['SO:0001036'], locus='SYN_00009', id='SYN9', pseudogene={'paralog': False}),
    feat(type='tRNA', sequence='contig_1', start=1790, stop=1850, strand='+', gene=None, product='tRNA-Xxx', amino_acid=None,
         anti_codon=None, score=20.0, db_xrefs=[], locus='SYN_00010', id='SYN10', truncated='3-prime'),
    # tmRNA with tag, forward and reverse
    feat(type='tmRNA', sequence='contig_1', start=1860, stop=2200, strand='+', gene='ssrA', product='transfer-messenger RNA, SsrA',
         db_xrefs=['SO:0000584'], locus='SYN_00011', id='SYN11',
         tag={'start': 1950, 'stop': 1985, 'aa': 'ANDENYALAA'}),
    feat(type='tmRNA', sequence='contig_2', start=100, stop=440, strand='-', gene='ssrA', product='transfer-messenger RNA, SsrA',
         db_xrefs=['SO:0000584'], locus='SYN_00012', id='SYN12', truncated='5-prime',
         tag={'start': 200, 'stop': 235, 'aa': 'ANDENYALAA'}),
    # rRNA with RFAM, gene None, truncated
    feat(type='rRNA', sequence='contig_1', start=2210, stop=2330, strand='+', gene='rrf', product='5S ribosomal RNA',
         db_xrefs=['RFAM:RF00001', 'SO:0000652', 'GO:0005840'], locus='SYN_00013', id='SYN13', evalue=1.2e-15, score=80.1),
    feat(type='rRNA', sequence='contig_2', start=500, stop=620, strand='-', gene=None, product='5S ribosomal RNA',
         db_xrefs=['RFAM:RF00001'], locus='SYN_00014', id='SYN14', evalue=0.0, score=10.0, truncated='5-prime'),
    # ncRNAs: each class, standard and non-standard gene symbols, truncated
    feat(type='ncRNA', sequence='contig_1', start=2340, stop=2400, strand='+', gene='rnpB', product='RNase P RNA',
         db_xrefs=['RFAM:RF00010', 'SO:0000386'], locus='SYN_00015', id='SYN15', evalue=3.4e-20, score=99.0,
         **{'class': ['RNase_P_RNA', 'SO:0000386']}),
    feat(type='ncRNA', sequence='contig_1', start=2410, stop=2460, strand='-', gene='isrA', product='antisense RNA',
         db_xrefs=['RFAM:RF00100'], locus='SYN_00016', id='SYN16', evalue=2e-05, score=40.0,
         **{'class': ['antisense_RNA', 'SO:0000644']}),
    feat(type='ncRNA', sequence='contig_1', start=2470, stop=2520, strand='+', gene='glmS_ribozyme', product='glmS ribozyme',
         db_xrefs=['RFAM:RF00234'], locus='SYN_00017', id='SYN17', evalue=123456789012345678.0, score=40.0, truncated='3-prime',
         **{'class': ['ribozyme', 'SO:0000374']}),
    feat(type='ncRNA', sequence='contig_1', start=2530, stop=2580, strand='+', gene='ffs', product='4.5S RNA',
         db_xrefs=['RFAM:RF00169'], locus='SYN_00018', id='SYN18', evalue=0.0001, score=40.0, **{'class': None}),
    # ncRNA regions: each regulatory class
    *[feat(type='ncRNA-region', sequence='contig_2', start=650 + 30 * i, stop=670 + 30 * i, strand='+', product=f'region {name}',
           db_xrefs=['RFAM:RF0%04d' % i, 'SO:0005836'], id=f'SYN_R{i}', evalue=1e-10 * (i + 1), score=20.0,
           **({'class': [name, so_id]} if so_id else {'class': None}), **({'truncated': '5-prime'} if i == 0 else {}))
      for i, (name, so_id) in enumerate([
          ('attenuator', 'SO:0000140'), ('riboswitch', 'SO:0000035'), ('RNA_thermometer', 'SO:0002168'),
          ('recoding_stimulatory_region', 'SO:1001268'), ('cis_regulatory_frameshift_element', 'SO:0001427'),
          ('ribosome_entry_site', 'SO:0000139'), ('response_element', 'SO:0002205'), ('none', None)])],
    # CRISPR with repeats and spacers
    feat(type='crispr', sequence='contig_1', start=2600, stop=2760, strand='?', product='CRISPR array with 3 repeats of length 30, consensus sequence ACGT and spacer length 20',
         db_xrefs=[], id='SYN_CRISPR1', repeat_consensus='ACGTACGTACGTACGTACGTACGTACGTAC',
         repeats=[{'start': 2600, 'stop': 2629, 'strand': '?'}, {'start': 2650, 'stop': 2679, 'strand': '?'}, {'start': 2700, 'stop': 2729, 'strand': '?'}],
         spacers=[{'start': 2630, 'stop': 2649, 'strand': '?', 'sequence': 'AAAACCCCGGGGTTTTAAAA'}, {'start': 2680, 'stop': 2699, 'strand': '?', 'sequence': 'CCCCGGGGTTTTAAAACCCC'}]),
    # gap and origins
    feat(type='gap', sequence='contig_1', start=2770, stop=2800, strand='+', length=31, id='SYN_GAP1'),
    feat(type='oriC', sequence='contig_1', start=2810, stop=2850, strand='?', product='origin of replication', id='SYN_ORIC1'),
    feat(type='oriT', sequence='contig_2', start=900, stop=950, strand='?', product='origin of transfer', id='SYN_ORIT1'),
    feat(type='oriV', sequence='contig_2', start=960, stop=1000, strand='?', product='origin of replication', id='SYN_ORIV1'),
    # CDS wrapping round the origin of circular contig_2
    feat(type='cds', sequence='contig_2', start=1150, stop=60, strand='+', frame=1, gene=None, product='Edge protein',
         db_xrefs=['UniRef:UniRef90_E'], locus='SYN_00019', id='SYN19', edge=True),
]

def add_sequences(features, contigs):
    """Add the nt, aa and derived fields Bakta writes for each feature."""
    from Bio.Seq import Seq
    for f in features:
        if f['type'] == 'gap':
            continue
        contig = contigs[f['sequence']]
        if f.get('edge'):
            nt = contig[f['start'] - 1:] + contig[:f['stop']]
        else:
            nt = contig[f['start'] - 1:f['stop']]
        if f['strand'] == '-':
            nt = str(Seq(nt).reverse_complement())
        f['nt'] = nt
        if f['type'] in ('cds', 'sorf'):
            aa = str(Seq(nt).translate(table=11)).rstrip('*')
            if f.get('truncated') != '5-prime':
                aa = 'M' + aa[1:]
            f['aa'] = aa
            f['aa_hexdigest'] = hashlib.md5(aa.encode()).hexdigest()
            if f['type'] == 'cds':
                f['start_type'] = nt[:3]
            if f['product'] == 'hypothetical protein':
                f['hypothetical'] = True


add_sequences(features, {'contig_1': contig1, 'contig_2': contig2})

data = {
    'genome': {'genus': 'Testus', 'species': 'syntheticus', 'strain': None, 'taxon': 'Testus syntheticus', 'complete': False, 'gram': '?', 'translation_table': 11},
    'stats': {'no_sequences': 2, 'size': len(contig1) + len(contig2)},
    'features': features,
    'sequences': [
        {'id': 'contig_1', 'description': '[gcode=11] [topology=linear]', 'nt': contig1, 'length': len(contig1), 'complete': False, 'type': 'contig', 'topology': 'linear', 'orig_id': '1', 'orig_description': ''},
        {'id': 'contig_2', 'description': '[gcode=11] [topology=circular]', 'nt': contig2, 'length': len(contig2), 'complete': True, 'type': 'plasmid', 'topology': 'circular', 'orig_id': '2', 'orig_description': ''},
    ],
    'run': {'start': '2026-01-01 00:00:00', 'end': '2026-01-01 00:10:00', 'duration': '10.00 min'},
    'version': {'bakta': '1.12.2', 'db': {'version': '6.0', 'type': 'full'}},
}
out.mkdir(parents=True, exist_ok=True)
(out / 'synthetic.bakta.json').write_text(json.dumps(data, indent=4))
(out / 'synthetic.fa').write_text(f'>1\n{contig1}\n>2\n{contig2}\n')
