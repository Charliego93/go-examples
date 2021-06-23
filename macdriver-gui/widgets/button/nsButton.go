package button

import (
	"github.com/progrium/macdriver/cocoa"
	"github.com/progrium/macdriver/core"
	"github.com/progrium/macdriver/objc"
)

var (
	nsButton = objc.Get("NSButton")
)

type NSButton struct {
	cocoa.NSView
}

func NewButtonWithFrame(frame core.NSRect) NSButton {
	return NSButton{cocoa.NSView{nsButton.Alloc().Send("initWithFrame:", frame)}}
}

func (b NSButton) SetTitle(title string) {
	b.Set("title:", core.String(title))
}

func (b NSButton) SetType() {
	b.Send("setButtonType:", core.NSUInteger(0))
}

func (b NSButton) SetAction(action func(object objc.Object)) {
	obj, selector := core.Callback(action)
	b.Set("target:", obj)
	b.Set("action:", selector)
}
