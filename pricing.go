// Package tollgate holds the files that the Tollgate binary embeds from the
// root of the repository. The program itself is in cmd/tollgate.
package tollgate

import (
	"bytes"
	_ "embed"
)

//go:embed pricing.yaml
var priceTable []byte

// PriceTable returns the content of pricing.yaml. Tollgate uses it when the
// configuration does not set pricing.file.
func PriceTable() []byte {
	return bytes.Clone(priceTable)
}
