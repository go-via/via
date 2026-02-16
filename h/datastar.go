package h

func DataInit(expression string) H {
	return Data("init", expression)
}

func DataEffect(expression string) H {
	return Data("effect", expression)
}

func DataIgnoreMorph() H {
	return Attr("data-ignore-morph")
}

func DataShow(expression string) H {
	return Data("show", expression)
}
