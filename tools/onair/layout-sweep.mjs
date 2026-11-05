// Manual host-Chrome On Air layout matrix; query fixtures live only in mock mode.
// Fresh fixture reloads suppress beforeunload; navigation guards have separate tests.
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
const [out, phase = "before", modes = "empty,one,many,long", surfaces = "all"] =
  process.argv.slice(2);
if (!out) throw new Error("Pass output directory");
mkdirSync(out + "/" + phase, { recursive: true });
const session = "w6-layout";
const cli = (...args) =>
  execFileSync("playwright-cli", ["-s=" + session, ...args], {
    encoding: "utf8",
    env: { ...process.env, PLAYWRIGHT_SKIP_BROWSER_GC: "1" },
    maxBuffer: 12e6,
  });
const records = [];
const resultFile =
  out +
  "/" +
  phase +
  (surfaces === "all" ? "" : "-" + surfaces) +
  "-results.json";
const results = existsSync(resultFile)
  ? JSON.parse(readFileSync(resultFile, "utf8")).filter(
      (r) => !modes.split(",").includes(r.mode),
    )
  : [];
try {
  cli("open", "http://localhost:5193/ether/livetv/looks");
  for (const mode of modes.split(","))
    for (const viewport of ["desktop", "phone"])
      for (const theme of ["dark", "light"]) {
        const code = `async page=>{
    page.setDefaultTimeout(8000);await page.setViewportSize(${JSON.stringify(viewport === "desktop" ? { width: 1440, height: 900 } : { width: 390, height: 844 })});
    await page.emulateMedia({colorScheme:${JSON.stringify(theme)},reducedMotion:"reduce"});
    await page.addInitScript(theme=>{localStorage.setItem('ether-theme',theme);const add=window.addEventListener;window.addEventListener=function(type,...args){if(type!=='beforeunload')return add.call(this,type,...args);};},${JSON.stringify(theme)});
    const rows=[],errors=[],findings=[];
    const mode=${JSON.stringify(mode)},prefix=${JSON.stringify(viewport + "-" + theme + "-" + mode)},base='http://localhost:5193/ether';
    const go=async(path)=>{await page.goto(base+path+(path.includes('?')?'&':'?')+'onair='+mode);await page.waitForTimeout(900);await page.locator('[data-brand-splash]').waitFor({state:'hidden'});await page.waitForTimeout(220);await page.evaluate(async()=>{const r=await import('/ether/src/pages/livetv/onair/scene-renderer.ts');await r.loadOnAirFonts();});await page.waitForFunction(()=>[...document.querySelectorAll('canvas')].every(c=>c.getContext('2d').getImageData(0,0,c.width,c.height).data.some(v=>v!==0)));};
    const capture=async(name,target)=>{
      if(target)await target.scrollIntoViewIfNeeded();await page.waitForTimeout(180);
      const file=prefix+'-'+name+'.png';await page.screenshot({path:${JSON.stringify(out + "/" + phase + "/")}+file});
      const overflow=await page.evaluate(()=>({page:document.documentElement.scrollWidth>innerWidth+1,dialogs:[...document.querySelectorAll('[role=dialog]')].filter(e=>e.getBoundingClientRect().width>0).map(e=>({width:e.getBoundingClientRect().width,scrollWidth:e.scrollWidth,clientWidth:e.clientWidth}))}));
      rows.push({file:${JSON.stringify(phase + "/")}+file,caption:prefix+' · '+name,overflow});
    };
    const run=async(name,fn)=>{try{await fn();}catch(error){errors.push({name,error:String(error)});}};
    await run('gallery',async()=>{await go('/livetv/looks');await page.getByRole('heading',{name:'Looks',exact:true}).waitFor();await capture('screen-defaults',page.getByRole('heading',{name:'Screens',exact:true}));await capture('gallery',page.getByRole('heading',{name:'Looks',exact:true}));});
    if(${JSON.stringify(surfaces)}==='gallery')return {rows,errors,findings};
    await run('Add',async()=>{await go('/livetv/looks');await page.getByRole('button',{name:'Add',exact:true}).click();await capture('add-menu');});
    await run('chooser',async()=>{await go('/livetv/looks?look=new');await page.getByRole('heading',{name:'New look',exact:true}).waitFor();await capture('look-chooser');});
    await run('look-inspector',async()=>{
      await go('/livetv/looks?look='+(mode==='empty'?'new':'nocturne'));
      if(mode==='empty')await page.getByRole('dialog').getByRole('button',{name:'Clean preview Clean',exact:true}).click();
      await page.getByLabel('Look name',{exact:true}).waitFor();
      for(const tab of ['Design','Describe','Layers','Assets']){
        const control=page.getByRole('tab',{name:tab,exact:true});await control.click();const panel=page.locator('aside').filter({has:control}).locator(':scope > div').last();await capture('look-'+tab.toLowerCase(),panel);
      }
    });
    await run('channel-look',async()=>{
      await go('/livetv?channel=v-movie-night');await page.getByRole('tab',{name:'Look',exact:true}).click();
      const picker=page.getByRole('button',{name:'Look',exact:true});await picker.click();await page.getByRole('menuitemradio',{name:'None',exact:true}).click();await capture('look-none',picker);
      if(mode!=='empty'){
        await picker.click();const option=page.getByRole('menuitemradio').filter({hasText:mode==='one'?'Nocturne':'The evening'}).first();
        if(mode==='long'||mode==='many')await option.click();else await page.getByRole('menuitemradio',{name:/Nocturne/}).click();
        await capture('look-selected',picker);
      }
      await picker.click();await capture('look-options',picker);
      const menu=page.getByRole('menu',{name:'Look',exact:true});
      findings.push({surface:'look-options',geometry:await menu.evaluate(el=>({height:el.clientHeight,scrollHeight:el.scrollHeight,width:el.clientWidth,scrollWidth:el.scrollWidth})),thumbnails:await menu.locator('canvas').evaluateAll(cs=>cs.map(c=>({width:c.getBoundingClientRect().width,height:c.getBoundingClientRect().height}))) });
    });
    await run('reel',async()=>{await go('/livetv/looks?reel='+(mode==='empty'?'new':'saturday'));const name=page.getByRole('textbox',{name:'Reel name',exact:true});await name.waitFor();await capture('reel',name);if(mode!=='empty'){const clip=page.getByRole('button',{name:/^Preview /}).first();await clip.click();await capture('reel-preview',clip);}});
    await run('recipe',async()=>{await go('/livetv?channel=v-personal');const tab=page.getByRole('tab',{name:'Recipe',exact:true});await tab.click();await capture('recipe',page.getByRole('switch',{name:'Continue watching',exact:true}));});
    await run('viewer',async()=>{await go('/viewers/2/limits');const toggle=page.getByRole('switch',{name:'Personal channel',exact:true});await toggle.waitFor();await capture('viewer',toggle);await toggle.press('Space');await capture('viewer-off',toggle);});
    return {rows,errors,findings};
  }`;
        const raw = cli("run-code", code);
        const a = raw.indexOf("### Result\n"),
          b = raw.indexOf("\n### Ran", a);
        if (a < 0 || b < 0) {
          results.push({ mode, viewport, theme, error: raw });
          continue;
        }
        const r = JSON.parse(raw.slice(a + 11, b).trim());
        records.push(...r.rows);
        results.push({
          mode,
          viewport,
          theme,
          errors: r.errors,
          findings: r.findings,
        });
        const captions = out + "/captions.json";
        let prior = existsSync(captions)
          ? JSON.parse(readFileSync(captions, "utf8"))
          : [];
        prior = prior.filter((r) => !records.some((n) => n.file === r.file));
        writeFileSync(
          captions,
          JSON.stringify([...prior, ...records], null, 2) + "\n",
        );
        writeFileSync(resultFile, JSON.stringify(results, null, 2) + "\n");
        console.log(
          mode,
          viewport,
          theme,
          "screens",
          r.rows.length,
          "errors",
          r.errors.length,
        );
      }
} finally {
  cli("close");
}
