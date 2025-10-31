package h

import gh "maragu.dev/gomponents/html"

func Href(v string) H {
	return gh.Href(v)
}

func Type(v string) H {
	return gh.Type(v)
}

func Src(v string) H {
	return gh.Src(v)
}

func ID(v string) H {
	return gh.ID(v)
}

func Value(v string) H {
	return gh.Value(v)
}

func Placeholder(v string) H {
	return gh.Placeholder(v)
}

// Data attributes automatically have their name prefixed with "data-".
func Data(name, v string) H {
	return gh.Data(name, v)
}
