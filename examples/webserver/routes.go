package main

import (
	_ "embed"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/soypat/lneto/http/httphi"
	"github.com/soypat/lneto/http/httpraw"
)

//go:embed index.html
var indexHTML string

//go:embed sixlines.html
var sixlinesHTML string

var scratchPool sync.Pool // Stores []byte buffers to avoid allocations in HTTP handlers.
const scratchSize = 128

func logRequest(h httphi.HandlerFunc) httphi.HandlerFunc {
	return func(exch *httphi.Exchange) {
		println(exch.RequestMethod().String(), " ", exch.MuxPattern())
		h(exch)
	}
}

func root(exch *httphi.Exchange) {
	exch.RespondString(httphi.StatusOK, "text/html", indexHTML)
}

func sixlines(exch *httphi.Exchange) {
	exch.RespondString(httphi.StatusOK, "text/html", sixlinesHTML)
}

const textplain = "text/plain; charset=UTF-8"

func LED_ON(exch *httphi.Exchange) {
	setLED(true)
	exch.RespondString(httphi.StatusOK, textplain, "led.High()")
}

func LED_OFF(exch *httphi.Exchange) {
	setLED(false)
	exch.RespondString(httphi.StatusOK, textplain, "led.Low()")
}

func hello(exch *httphi.Exchange) {
	exch.RespondString(httphi.StatusOK, textplain, "hello")
}

var counter atomic.Int64

func cnt(exch *httphi.Exchange) {
	scratch := scratchPool.Get().([]byte)
	defer scratchPool.Put(scratch)
	switch exch.RequestMethod() {
	case httphi.MethPost: // POST
		var form httpraw.Form
		form.Reset(scratch, 2) // 2 query values max.
		const parseURL, prioritizeURL = true, false
		err := exch.RequestParseForm(&form, parseURL, prioritizeURL)
		if err != nil {
			exch.Respond(httphi.StatusInternalServerError, "", nil)
			return
		}
		c := form.Get("cnt")
		if len(c) > 0 {
			i64, _ := strconv.ParseInt(string(c), 0, 0)
			counter.Store(i64)
			println("set counter", i64)
		}
	}
	json := append(scratch[:0], `{"cnt": `...)
	json = strconv.AppendInt(json, counter.Load(), 10)
	json = append(json, '}')
	exch.Respond(httphi.StatusOK, "application/json", json)
}
