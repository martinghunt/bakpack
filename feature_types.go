package bakpack

import (
	"fmt"
	"sort"
)

const (
	gffAttrKindProtein          = "protein"
	gffAttrKindGap              = "gap"
	gffAttrKindOrigin           = "origin"
	gffAttrKindRNA              = "rna"
	gffAttrKindRegulatoryRegion = "regulatory_region"
	gffAttrKindTRNA             = "trna"
	gffAttrKindCRISPR           = "crispr"
)

type baktaFeatureTypeInfo struct {
	Name       string
	GFF3Source string
	GFF3Type   string
	GFF3Attr   string
	Protein    bool
	Gap        bool
	Infernal   bool
	StartCodon bool
	CRISPR     bool
}

var baktaFeatureTypes = map[string]baktaFeatureTypeInfo{
	"cds": {
		Name:       "cds",
		GFF3Source: "Pyrodigal",
		GFF3Type:   "CDS",
		GFF3Attr:   gffAttrKindProtein,
		Protein:    true,
		StartCodon: true,
	},
	"sorf": {
		Name:       "sorf",
		GFF3Source: "Bakta",
		GFF3Type:   "CDS",
		GFF3Attr:   gffAttrKindProtein,
		Protein:    true,
	},
	"gap": {
		Name:       "gap",
		GFF3Source: "Bakta",
		GFF3Type:   "gap",
		GFF3Attr:   gffAttrKindGap,
		Gap:        true,
	},
	"oriC": {
		Name:       "oriC",
		GFF3Source: "BLAST+",
		GFF3Type:   "oriC",
		GFF3Attr:   gffAttrKindOrigin,
	},
	"oriT": {
		Name:       "oriT",
		GFF3Source: "BLAST+",
		GFF3Type:   "oriT",
		GFF3Attr:   gffAttrKindOrigin,
	},
	"ncRNA": {
		Name:       "ncRNA",
		GFF3Source: "Infernal",
		GFF3Type:   "ncRNA",
		GFF3Attr:   gffAttrKindRNA,
		Infernal:   true,
	},
	"ncRNA-region": {
		Name:       "ncRNA-region",
		GFF3Source: "Infernal",
		GFF3Type:   "regulatory_region",
		GFF3Attr:   gffAttrKindRegulatoryRegion,
		Infernal:   true,
	},
	"rRNA": {
		Name:       "rRNA",
		GFF3Source: "Infernal",
		GFF3Type:   "rRNA",
		GFF3Attr:   gffAttrKindRNA,
		Infernal:   true,
	},
	"tRNA": {
		Name:       "tRNA",
		GFF3Source: "tRNAscan-SE",
		GFF3Type:   "tRNA",
		GFF3Attr:   gffAttrKindTRNA,
	},
	"tmRNA": {
		Name:       "tmRNA",
		GFF3Source: "Aragorn",
		GFF3Type:   "tmRNA",
		GFF3Attr:   gffAttrKindRNA,
	},
	"crispr": {
		Name:       "crispr",
		GFF3Source: "PILER-CR",
		GFF3Type:   "CRISPR",
		GFF3Attr:   gffAttrKindCRISPR,
		CRISPR:     true,
	},
}

// SupportedBaktaFeatureTypes returns the Bakta feature types currently handled
// by reduction, archive compression/extraction, and GFF3 rendering.
func SupportedBaktaFeatureTypes() []string {
	types := make([]string, 0, len(baktaFeatureTypes))
	for featureType := range baktaFeatureTypes {
		types = append(types, featureType)
	}
	sort.Strings(types)
	return types
}

func baktaFeatureType(featureType string) (baktaFeatureTypeInfo, bool) {
	info, ok := baktaFeatureTypes[featureType]
	return info, ok
}

func isSupportedBaktaFeatureType(featureType string) bool {
	_, ok := baktaFeatureTypes[featureType]
	return ok
}

func validateBaktaFeatureTypes(data map[string]any) error {
	features, _ := data["features"].([]any)
	for i, item := range features {
		feature, ok := item.(map[string]any)
		if !ok {
			continue
		}
		featureType, ok := feature["type"].(string)
		if !ok || featureType == "" {
			return fmt.Errorf("feature %d is missing Bakta feature type", i)
		}
		if !isSupportedBaktaFeatureType(featureType) {
			return unsupportedBaktaFeatureTypeError(featureType)
		}
	}
	return nil
}

func validateBaktaJSONFeatureTypes(data []byte) error {
	root, err := DecodeJSON(data)
	if err != nil {
		return err
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return fmt.Errorf("Bakta JSON root is not an object")
	}
	return validateBaktaFeatureTypes(obj)
}

func validateFeatureTypeFieldCodec(codec FieldCodec) error {
	if codec.Field != "type" {
		return nil
	}
	switch codec.Kind {
	case "const_string":
		value, ok := codec.Value.(string)
		if !ok {
			return fmt.Errorf("archive feature type codec has non-string value")
		}
		if !isSupportedBaktaFeatureType(value) {
			return unsupportedBaktaFeatureTypeError(value)
		}
	case "enum_string", "nullable_enum_string":
		for _, value := range codec.Values {
			if !isSupportedBaktaFeatureType(value) {
				return unsupportedBaktaFeatureTypeError(value)
			}
		}
	case "const_null":
		return fmt.Errorf("archive feature type codec has null value")
	}
	return nil
}

func unsupportedBaktaFeatureTypeError(featureType string) error {
	return fmt.Errorf("unsupported Bakta feature type %q", featureType)
}
