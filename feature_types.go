package bakpack

import (
	"fmt"
	"sort"
)

type baktaFeatureTypeInfo struct {
	Name       string
	Protein    bool
	Gap        bool
	StartCodon bool
}

var baktaFeatureTypes = map[string]baktaFeatureTypeInfo{
	"cds": {
		Name:       "cds",
		Protein:    true,
		StartCodon: true,
	},
	"sorf": {
		Name:    "sorf",
		Protein: true,
	},
	"gap": {
		Name: "gap",
		Gap:  true,
	},
	"oriC": {
		Name: "oriC",
	},
	// Bakta reports replication origins on plasmids as oriV but writes them
	// with the oriC SO term in GFF3.
	"oriV": {
		Name: "oriV",
	},
	"oriT": {
		Name: "oriT",
	},
	"ncRNA": {
		Name: "ncRNA",
	},
	"ncRNA-region": {
		Name: "ncRNA-region",
	},
	"rRNA": {
		Name: "rRNA",
	},
	"tRNA": {
		Name: "tRNA",
	},
	"tmRNA": {
		Name: "tmRNA",
	},
	"crispr": {
		Name: "crispr",
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
	case fieldCodecConstString:
		value, ok := codec.Value.(string)
		if !ok {
			return fmt.Errorf("archive feature type codec has non-string value")
		}
		if !isSupportedBaktaFeatureType(value) {
			return unsupportedBaktaFeatureTypeError(value)
		}
	case fieldCodecEnumString, fieldCodecNullableEnumString:
		for _, value := range codec.Values {
			if !isSupportedBaktaFeatureType(value) {
				return unsupportedBaktaFeatureTypeError(value)
			}
		}
	case fieldCodecConstNull:
		return fmt.Errorf("archive feature type codec has null value")
	}
	return nil
}

func unsupportedBaktaFeatureTypeError(featureType string) error {
	return fmt.Errorf("unsupported Bakta feature type %q", featureType)
}
