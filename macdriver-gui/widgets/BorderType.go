package widgets

import "github.com/progrium/macdriver/core"

type NSBorderType core.NSUInteger

const (
	NoBorderType     NSBorderType = iota //No border.
	LineBorderType                       // A black line border around the view.
	BezelBorderType                      // A concave border that makes the view look sunken.
	GrooveBorderType                     // A thin border that looks etched around the image.
)
