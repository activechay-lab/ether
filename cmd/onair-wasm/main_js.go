//go:build js && wasm

// onair-wasm exposes a synchronous pure renderer; the caller may host it in a worker.
package main

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"syscall/js"
)

func main() {
	var buffer js.Value
	render := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) != 5 {
			return "render expects docJSON, bindingsJSON, t, width, height"
		}
		doc, e := scene.Decode([]byte(args[0].String()))
		if e != nil {
			return e.Error()
		}
		var bind raster.Bindings
		if e = json.Unmarshal([]byte(args[1].String()), &bind); e != nil {
			return e.Error()
		}
		w, h := args[3].Int(), args[4].Int()
		if w < 1 || h < 1 || w > 4096 || h > 2160 {
			return "invalid dimensions"
		}
		im := raster.Render(doc, bind, args[2].Float(), w, h)
		if buffer.IsUndefined() || buffer.Length() != len(im.Pix) {
			buffer = js.Global().Get("Uint8ClampedArray").New(len(im.Pix))
		}
		js.CopyBytesToJS(buffer, im.Pix)
		return buffer
	})
	js.Global().Set("onairRender", render)
	select {}
}
