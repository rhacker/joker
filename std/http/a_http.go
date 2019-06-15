// This file is generated by generate-std.joke script. Do not edit manually!

package http

import (
	. "github.com/candid82/joker/core"
)

var httpNamespace = GLOBAL_ENV.EnsureNamespace(MakeSymbol("joker.http"))



var send_ Proc = func(_args []Object) Object {
	_c := len(_args)
	switch {
	case _c == 1:
		request := ExtractMap(_args, 0)
		_res := sendRequest(request)
		return _res

	default:
		PanicArity(_c)
	}
	return NIL
}

var start_file_server_ Proc = func(_args []Object) Object {
	_c := len(_args)
	switch {
	case _c == 2:
		addr := ExtractString(_args, 0)
		root := ExtractString(_args, 1)
		_res := startFileServer(addr, root)
		return _res

	default:
		PanicArity(_c)
	}
	return NIL
}

var start_server_ Proc = func(_args []Object) Object {
	_c := len(_args)
	switch {
	case _c == 2:
		addr := ExtractString(_args, 0)
		handler := ExtractCallable(_args, 1)
		_res := startServer(addr, handler)
		return _res

	default:
		PanicArity(_c)
	}
	return NIL
}

func init() {

	httpNamespace.ResetMeta(MakeMeta(nil, "Provides HTTP client and server implementations", "1.0"))

	
	httpNamespace.InternVar("send", send_,
		MakeMeta(
			NewListFrom(NewVectorFrom(MakeSymbol("request"))),
			`Sends an HTTP request and returns an HTTP response.
  request is a map with the following keys:
  - url (string)
  - method (string, keyword or symbol, defaults to :get)
  - body (string)
  - host (string, overrides Host header if provided)
  - headers (map).
  All keys except for url are optional.
  response is a map with the following keys:
  - status (int)
  - body (string)
  - headers (map)
  - content-length (int)`, "1.0"))

	httpNamespace.InternVar("start-file-server", start_file_server_,
		MakeMeta(
			NewListFrom(NewVectorFrom(MakeSymbol("addr"), MakeSymbol("root"))),
			`Starts HTTP server on the TCP network address addr that
  serves HTTP requests with the contents of the file system rooted at root.`, "1.0"))

	httpNamespace.InternVar("start-server", start_server_,
		MakeMeta(
			NewListFrom(NewVectorFrom(MakeSymbol("addr"), MakeSymbol("handler"))),
			`Starts HTTP server on the TCP network address addr.`, "1.0"))

}
