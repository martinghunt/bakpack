package bakpack

import (
	"fmt"
	"time"

	"github.com/martinghunt/faqt/insdc"
)

// buildBaktaINSDCRecords returns the GenBank/EMBL records for data, one per
// sequence, dated date.
func buildBaktaINSDCRecords(data map[string]any, compliant bool, date time.Time, warn func(string)) ([]*insdc.Record, error) {
	return nil, fmt.Errorf("GenBank and EMBL output are not implemented yet")
}
