package coder

import (
	_ "embed"
	"sync"

	"github.com/dop251/goja"
)

// Trial arms for doc/experiments/2026-09-run-code-lodash, selected at link
// time with -ldflags "-X dbohdan.com/strument/internal/coder.codeArm=NAME".
// Empty is the baseline and changes nothing. This file comes out when the
// trial ends, whatever the result; a winning arm is then built properly.
//
//	polyfill  Object.groupBy and Map.groupBy (ES2024), which goja lacks; no
//	          description change, since both are standard names
//	lodash    the polyfill, plus Lodash 4.17.21 as _, named in one line of
//	          run_code's description
var codeArm string

//go:embed codearm_lodash.min.js
var lodashSource string

// groupByPolyfill follows the ES2024 shape: a null-prototype object for
// Object.groupBy, a Map for Map.groupBy, the callback given (item, index).
const groupByPolyfill = `(function () {
  if (typeof Object.groupBy !== "function") {
    Object.defineProperty(Object, "groupBy", {configurable: true, writable: true, value: function (items, fn) {
      var out = Object.create(null), i = 0;
      for (var item of items) {
        var k = fn(item, i++);
        (out[k] || (out[k] = [])).push(item);
      }
      return out;
    }});
  }
  if (typeof Map.groupBy !== "function") {
    Object.defineProperty(Map, "groupBy", {configurable: true, writable: true, value: function (items, fn) {
      var out = new Map(), i = 0;
      for (var item of items) {
        var k = fn(item, i++);
        if (!out.has(k)) out.set(k, []);
        out.get(k).push(item);
      }
      return out;
    }});
  }
})();`

// codeArmDoc is arm C's description line. It is worded as an addition inside
// a program, not as a list of what can be called, so it cannot be read as the
// tool list (doc/experiments/2026-09-tool-disclosure).
const codeArmDoc = "\n\nInside a program, Lodash 4 is loaded as _: for example " +
	"_.countBy(rows, \"kind\"), _.groupBy, _.sortBy, _.uniq, _.keyBy and _.sumBy."

func codeArmDescription() string {
	if codeArm == "lodash" {
		return codeArmDoc
	}
	return ""
}

var (
	lodashOnce sync.Once
	lodashProg *goja.Program
	errLodash  error
)

// installCodeArm prepares a program's runtime for the arm. Lodash is compiled
// once per process and run in each fresh runtime, about 5 ms.
func installCodeArm(vm *goja.Runtime) error {
	if codeArm != "polyfill" && codeArm != "lodash" {
		return nil
	}
	if _, err := vm.RunString(groupByPolyfill); err != nil {
		return err
	}
	if codeArm != "lodash" {
		return nil
	}
	lodashOnce.Do(func() {
		lodashProg, errLodash = goja.Compile("lodash.min.js", lodashSource, false)
	})
	if errLodash != nil {
		return errLodash
	}
	_, err := vm.RunProgram(lodashProg)
	return err
}
