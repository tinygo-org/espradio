package main

import (
	_ "embed"
	"math/rand"
	"strconv"

	"github.com/soypat/lneto/http/httphi"
)

//go:embed index.html
var indexHTML string

const textplain string = "text/plain; charset=UTF-8"

func logRequest(handler httphi.HandlerFunc) httphi.HandlerFunc {
	return func(exch *httphi.Exchange) {
		println("[HTTP]", exch.RequestMethod().String(), exch.MuxPattern())
		handler(exch)
	}
}

func requireMethod(exch *httphi.Exchange, method httphi.Method) bool {
	if exch.RequestMethod() != method {
		exch.RespondString(httphi.StatusMethodNotAllowed, textplain, "Method not allowed")
		return false
	}
	return true
}

func root(exch *httphi.Exchange) {
	if !requireMethod(exch, httphi.MethGet) {
		return
	}

	exch.RespondString(httphi.StatusOK, "text/html; charset=utf-8", indexHTML)
}

func on(exch *httphi.Exchange) {
	if !requireMethod(exch, httphi.MethPost) {
		return
	}

	setLED(true)
	exch.RespondString(httphi.StatusOK, textplain, "on")
}

func off(exch *httphi.Exchange) {
	if !requireMethod(exch, httphi.MethPost) {
		return
	}

	setLED(false)
	exch.RespondString(httphi.StatusOK, textplain, "off")
}

func msg(exch *httphi.Exchange) {
	if !requireMethod(exch, httphi.MethPost) {
		return
	}

	const maxBody = 256
	var buf [maxBody]byte

	cl := exch.RequestHeader("Content-Length")
	if len(cl) > 0 {
		n, err := strconv.Atoi(string(cl))
		if err == nil && n > maxBody {
			exch.RespondString(httphi.StatusRequestEntityTooLarge, textplain, "body too large")
			return
		}
	}

	n, err := exch.ReadBody(buf[:])
	if err != nil {
		exch.RespondString(httphi.StatusBadRequest, textplain, "read error")
		return
	}
	println("[MSG]", string(buf[:n]))

	exch.RespondString(httphi.StatusOK, textplain, "received")
}

func hello(exch *httphi.Exchange) {
	if !requireMethod(exch, httphi.MethGet) {
		return
	}

	exch.RespondString(httphi.StatusOK, textplain, "Hello from TinyGo!")
}

func random(exch *httphi.Exchange) {
	if !requireMethod(exch, httphi.MethGet) {
		return
	}

	n := rand.Int()
	exch.RespondString(httphi.StatusOK, textplain, strconv.Itoa(n))
}
