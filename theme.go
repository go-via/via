package via

// themeCSS is the classless stylesheet WithTheme injects. It styles bare
// semantic tags only — no class hooks — so a View stays class-free and still
// looks intentional: a centered reading column, a system font stack, hairline
// borders, and a sticky bottom composer for forms.
const themeCSS = `
:root{--ink:#16202b;--muted:#5b6775;--line:#e4e9ef;--accent:#0e8f9e;--bg:#fbfcfd}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font:16px/1.55 system-ui,-apple-system,Segoe UI,Roboto,sans-serif}
body>div{max-width:46rem;margin:0 auto;min-height:100vh;display:flex;flex-direction:column;padding:1.5rem 1.25rem}
h1{font-size:1.4rem;letter-spacing:-.02em;margin:.2rem 0 1rem;padding-bottom:.6rem;border-bottom:1px solid var(--line)}
ul{list-style:none;margin:0;padding:0;flex:1;overflow-y:auto}
li{padding:.4rem .1rem;border-bottom:1px solid var(--line)}
li b{color:var(--accent)}
form{display:flex;gap:.5rem;align-items:center;position:sticky;bottom:0;background:var(--bg);padding-top:.75rem}
label{color:var(--muted);font-size:.85rem;display:flex;gap:.35rem;align-items:center}
input{flex:1;font:inherit;padding:.5rem .65rem;border:1px solid var(--line);border-radius:.5rem;background:#fff;color:inherit}
input:focus{outline:2px solid var(--accent);outline-offset:1px}
button{font:inherit;font-weight:600;padding:.5rem .9rem;border:0;border-radius:.5rem;background:var(--accent);color:#fff;cursor:pointer}
button:hover{filter:brightness(1.05)}
`

// themeStyle returns the nonce'd <style> tag for the head, or "" when the theme
// is off.
func themeStyle(on bool, nonce string) string {
	if !on {
		return ""
	}
	return `<style nonce="` + nonce + `">` + themeCSS + `</style>`
}
