// want +2 `canonical import path comment is ignored in module mode`

package importcommentmod // import "example.com/importcommentmod"

import "fmt"

var _ = fmt.Sprint
