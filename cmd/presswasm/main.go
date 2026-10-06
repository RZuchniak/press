//go:build js && wasm

package main

import (
	"press/internal/browser"
	"syscall/js"
)

func main() {
	bridge := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeString {
			return browser.HandleJSON("")
		}
		return browser.HandleJSON(args[0].String())
	})
	js.Global().Set("pressWasm", bridge)
	if ready := js.Global().Get("pressWasmReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}
	select {} // Keep exported callbacks alive for subsequent editor requests.
}
