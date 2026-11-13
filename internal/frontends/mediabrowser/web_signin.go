package mediabrowser

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func (i *instance) webSigninRoutes() error {
	if i.protocol != "jellyfin" {
		return nil
	}
	return i.router.Handle("POST", "/Ether/WebSignIn", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var b struct{ Code, Browser, Existing string }
		if r.Header.Get("Content-Type") != "application/json" || decode(r, &b) != nil {
			http.Error(w, "Invalid request", 400)
			return
		}
		service, ok := i.host.Identity.(interface {
			RedeemWebCode(context.Context, string, string, string) (string, int64, error)
		})
		if !ok {
			http.Error(w, "Sign-in unavailable", 503)
			return
		}
		token, viewer, err := service.RedeemWebCode(i.identityContext(r), b.Code, b.Browser, b.Existing)
		if err != nil {
			identityError(w, err)
			return
		}
		user, err := ids.GUID(ids.User, viewer)
		if err != nil {
			http.Error(w, "Sign-in unavailable", 503)
			return
		}
		jsonReply(w, map[string]string{"AccessToken": token, "UserId": user, "Id": i.info.ID})
	}))
}

// Codes travel in the fragment so HTTP access logs and referrers never see them.
// This page runs on the player origin, the only origin allowed to write its store.
func webSigninPage(w http.ResponseWriter, r *http.Request) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		http.Error(w, "Sign-in unavailable", 503)
		return
	}
	nonce := base64.StdEncoding.EncodeToString(raw[:])
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Ether</title><p id="status">Opening the web player…</p><a id="back" href="/ether/">Back to Ether</a><script nonce="%s">
(async()=>{
 const args=new URLSearchParams(location.hash.slice(1));
 const code=args.get('code');
 history.replaceState(null,'',location.pathname);
 try { const back=new URL(args.get('console')); if(['http:','https:'].includes(back.protocol)&&!back.username&&!back.password) document.getElementById('back').href=back.href; }catch{}
 try{
 const base=location.origin+location.pathname.replace(/\/web\/ether-signin$/,'');
 let browser=localStorage.getItem('_deviceId2');
 if(!browser){ const bytes=new Uint8Array(24);crypto.getRandomValues(bytes);browser=Array.from(bytes,b=>b.toString(16).padStart(2,'0')).join('');localStorage.setItem('_deviceId2',browser); }
 let credentials;try{credentials=JSON.parse(localStorage.getItem('jellyfin_credentials')||'{}');}catch{credentials={};}
 const servers=Array.isArray(credentials.Servers)?credentials.Servers:[];
 const old=servers.find(s=>s.ManualAddress===base||s.LocalAddress===base);
 const res=await fetch(base+'/Ether/WebSignIn',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({Code:code,Browser:browser,Existing:old?.AccessToken||''}),credentials:'omit'});
 if(!res.ok)throw Error();
 const entry=await res.json();
 entry.ManualAddress=base;entry.LocalAddress=base;entry.DateLastAccessed=Date.now();
 credentials.Servers=[...servers.filter(s=>s.Id!==entry.Id),entry];
 localStorage.setItem('jellyfin_credentials',JSON.stringify(credentials));
 localStorage.setItem('enableAutoLogin','true');
 location.replace(base+'/web/#/home.html');
 }catch{document.getElementById('status').textContent='This sign-in expired or could not finish. Go back to Ether and choose Watch again.';}
})();</script>`, nonce)
}
