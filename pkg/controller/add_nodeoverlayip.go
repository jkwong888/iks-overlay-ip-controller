package controller

import (
	"github.com/jkwong888/iks-overlay-ip-controller/pkg/controller/nodeoverlayip"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, nodeoverlayip.Add)
}
