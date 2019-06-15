// This file is generated by generate-std.joke script. Do not edit manually!

package math

import (
	. "github.com/candid82/joker/core"
	"math"
)

var mathNamespace = GLOBAL_ENV.EnsureNamespace(MakeSymbol("joker.math"))

var pi_ = MakeDouble(math.Pi)

var cos_ Proc = func(_args []Object) Object {
	_c := len(_args)
	switch {
	case _c == 1:
		x := ExtractNumber(_args, 0)
		_res := math.Cos(x.Double().D)
		return MakeDouble(_res)

	default:
		PanicArity(_c)
	}
	return NIL
}

var hypot_ Proc = func(_args []Object) Object {
	_c := len(_args)
	switch {
	case _c == 2:
		p := ExtractNumber(_args, 0)
		q := ExtractNumber(_args, 1)
		_res := math.Hypot(p.Double().D, q.Double().D)
		return MakeDouble(_res)

	default:
		PanicArity(_c)
	}
	return NIL
}

var sin_ Proc = func(_args []Object) Object {
	_c := len(_args)
	switch {
	case _c == 1:
		x := ExtractNumber(_args, 0)
		_res := math.Sin(x.Double().D)
		return MakeDouble(_res)

	default:
		PanicArity(_c)
	}
	return NIL
}

func init() {

	mathNamespace.ResetMeta(MakeMeta(nil, "Provides basic constants and mathematical functions.", "1.0"))

	mathNamespace.InternVar("pi", pi_,
		MakeMeta(
			nil,
			`Pi`, "1.0"))

	mathNamespace.InternVar("cos", cos_,
		MakeMeta(
			NewListFrom(NewVectorFrom(MakeSymbol("x"))),
			`Returns the cosine of the radian argument x.`, "1.0"))

	mathNamespace.InternVar("hypot", hypot_,
		MakeMeta(
			NewListFrom(NewVectorFrom(MakeSymbol("p"), MakeSymbol("q"))),
			`Returns Sqrt(p*p + q*q), taking care to avoid unnecessary overflow and underflow.`, "1.0"))

	mathNamespace.InternVar("sin", sin_,
		MakeMeta(
			NewListFrom(NewVectorFrom(MakeSymbol("x"))),
			`Returns the sine of the radian argument x.`, "1.0"))

}
