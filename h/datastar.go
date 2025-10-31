package h

import "fmt"

type OnClickOpts string

func OnClick(actionid string, opt ...OnClickOpts) H {
	return Data("on:click", fmt.Sprintf("@get('/_action/%s')", actionid))
}
