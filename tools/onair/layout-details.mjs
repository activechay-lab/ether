// Extra manual interaction states for the layout sweep (host playwright-cli).
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
const [out, phase = "before", surface = "all"] = process.argv.slice(2);
const cli = (...a) =>
  execFileSync("playwright-cli", ["-s=w6-details", ...a], {
    encoding: "utf8",
    env: { ...process.env, PLAYWRIGHT_SKIP_BROWSER_GC: "1" },
    maxBuffer: 8e6,
  });
mkdirSync(out + "/" + phase, { recursive: true });
const rows = [],
  results = [];
try {
  cli("open", "http://localhost:5193/ether/livetv/looks");
  for (const phone of [false, true])
    for (const theme of ["dark", "light"]) {
      const code = `async page=>{
 page.setDefaultTimeout(8000);await page.setViewportSize(${JSON.stringify(phone ? { width: 390, height: 844 } : { width: 1440, height: 900 })});await page.emulateMedia({colorScheme:${JSON.stringify(theme)},reducedMotion:'reduce'});
 await page.addInitScript(theme=>{localStorage.setItem('ether-theme',theme);const add=window.addEventListener;window.addEventListener=function(type,...args){if(type!=='beforeunload')return add.call(this,type,...args);};},${JSON.stringify(theme)});
 const rows=[],errors=[],bounds=[];
 const go=async p=>{await page.goto('http://localhost:5193/ether'+p+(p.includes('?')?'&':'?')+'onair=long');await page.waitForTimeout(1000);await page.locator('[data-brand-splash]').waitFor({state:'hidden'});await page.evaluate(async()=>{const r=await import('/ether/src/pages/livetv/onair/scene-renderer.ts');await r.loadOnAirFonts();});await page.waitForFunction(()=>[...document.querySelectorAll('canvas')].every(c=>c.getContext('2d').getImageData(0,0,c.width,c.height).data.some(v=>v!==0)));};
 const capture=async name=>{await page.waitForTimeout(200);const file=${JSON.stringify(phase + "/" + (phone ? "phone" : "desktop") + "-" + theme + "-details-")}+name+'.png';await page.screenshot({path:${JSON.stringify(out + "/")}+file});rows.push({file,caption:${JSON.stringify((phone ? "390×844" : "1440×900") + " " + theme + " · ")}+name});bounds.push({name,panels:await page.locator('[role=dialog]').evaluateAll(es=>es.map(e=>{const r=e.getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,viewport:innerWidth,height:innerHeight};}))});};
 const run=async(name,fn)=>{try{await fn();}catch(e){errors.push({name,error:String(e)});}};
 await run('gallery',async()=>{await go('/livetv/looks');await page.getByRole('heading',{name:'Looks',exact:true}).evaluate(e=>e.scrollIntoView({block:'start'}));await capture('gallery-names');});
 if(${JSON.stringify(surface)}==='gallery')return {rows,errors,bounds};
 await run('chooser',async()=>{await go('/livetv/looks?look=new');await page.getByRole('heading',{name:'Duplicate a look',exact:true}).evaluate(e=>e.scrollIntoView({block:'start'}));await capture('duplicate-long-names');});
 await run('colour',async()=>{await go('/livetv/looks?look=nocturne');const button=page.getByRole('button',{name:'Background',exact:true});await button.scrollIntoViewIfNeeded();await button.click();await capture('colour-popup');});
 await run('reel-rename',async()=>{await go('/livetv/looks?reel=saturday');const name=page.getByRole('textbox',{name:'Reel name',exact:true});await name.fill('A wonderfully long renamed reel for the evening broadcast collection');await page.getByRole('button',{name:/^Clip actions for/}).first().click();await page.getByRole('button',{name:'Rename',exact:true}).click();await capture('reel-rename-dirty');});
 await run('recipe',async()=>{await go('/livetv?channel=v-personal');await page.getByRole('tab',{name:'Recipe',exact:true}).click();const toggle=page.getByRole('switch',{name:'Your picks (collections)',exact:true});await toggle.scrollIntoViewIfNeeded();if(!await toggle.isChecked())await toggle.press('Space');await page.getByRole('button',{name:'Your picks (collections)',exact:true}).click();await capture('recipe-collections');await page.keyboard.press('Escape');const more=page.getByRole('button',{name:'More options',exact:true}).first();await more.click();await more.scrollIntoViewIfNeeded();await capture('recipe-options');});
 await run('viewer-name',async()=>{await go('/viewers/2/limits');await page.getByRole('button',{name:'More options: Live TV',exact:true}).click();const name=page.getByRole('textbox',{name:'Personal channel name',exact:true});await name.fill('The nostalgic evening adventures and familiar favourites channel');await name.scrollIntoViewIfNeeded();await capture('viewer-long-name');});
 await run('channel-screens',async()=>{await go('/livetv?channel=v-movie-night');await page.getByRole('tab',{name:'Look',exact:true}).click();const label=page.getByRole('heading',{name:'Screens',exact:true});await label.evaluate(e=>e.scrollIntoView({block:'start'}));await capture('channel-screen-pickers');});
 return {rows,errors,bounds};
}`;
      const raw = cli("run-code", code);
      const a = raw.indexOf("### Result\n"),
        b = raw.indexOf("\n### Ran", a);
      if (a < 0 || b < 0) throw Error(raw);
      const r = JSON.parse(raw.slice(a + 11, b).trim());
      rows.push(...r.rows);
      results.push({ phone, theme, errors: r.errors, bounds: r.bounds });
      console.log(phone, theme, r.rows.length, r.errors.length);
    }
} finally {
  cli("close");
}
const p = out + "/captions.json";
const prior = existsSync(p) ? JSON.parse(readFileSync(p, "utf8")) : [];
writeFileSync(
  p,
  JSON.stringify(
    [...prior.filter((r) => !rows.some((n) => n.file === r.file)), ...rows],
    null,
    2,
  ) + "\n",
);
writeFileSync(
  out +
    "/" +
    phase +
    (surface === "all" ? "-details" : "-details-" + surface) +
    ".json",
  JSON.stringify(results, null, 2) + "\n",
);
