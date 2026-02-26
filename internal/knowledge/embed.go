package knowledge

import _ "embed"

//go:embed docs/base.md
var baseDoc string

//go:embed docs/challenges.md
var challengesDoc string

//go:embed docs/platform.md
var platformDoc string

//go:embed docs/apis.md
var apisDoc string

// Embedded returns the raw embedded documents for inspection.
func Embedded() (base, challenges, platform, apis string) {
	return baseDoc, challengesDoc, platformDoc, apisDoc
}
