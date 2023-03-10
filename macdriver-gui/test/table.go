package main

import (
	"github.com/progrium/macdriver/cocoa"
	"github.com/progrium/macdriver/core"
	"github.com/progrium/macdriver/objc"
)

type NSTableView struct {
	cocoa.NSView
}

var NSTableView_ = objc.Get("NSTableView")

func NewNSTableView(frame core.NSRect) NSTableView {
	return NSTableView{NSView: cocoa.NSView{}}
}
