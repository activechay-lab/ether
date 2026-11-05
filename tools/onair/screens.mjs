// Host playwright-cli capture of the real console. The before images remain in
// thread-storage/v035/screens/w1-looks and w2-channel, as supplied by the owner.
import { execFileSync } from 'node:child_process';
import {mkdirSync,writeFileSync} from 'node:fs';
const out=process.argv[2];if(!out)throw new Error('Pass screenshot directory');mkdirSync(out,{recursive:true});
const cli=(...args)=>execFileSync('playwright-cli',['-s=w6-screens',...args],{encoding:'utf8',env:{...process.env,PLAYWRIGHT_SKIP_BROWSER_GC:'1'},maxBuffer:4e6});
const code=`async page => {
 await page.setViewportSize({width:1440,height:900});await page.emulateMedia({colorScheme:'dark'});
 const captions=[];
 await page.goto('http://localhost:5193/ether/livetv/looks');
 await page.getByRole('heading',{name:'Looks',exact:true}).waitFor();
 await page.locator('[data-brand-splash]').waitFor({state:'hidden'});
 await page.getByRole('heading',{name:'Nocturne',exact:true}).waitFor();
 await page.waitForFunction(()=>[...document.querySelectorAll('canvas')].some(c=>c.getContext('2d').getImageData(0,0,c.width,c.height).data.some(v=>v!==0)));
 await page.waitForTimeout(250);
 await page.screenshot({path:${JSON.stringify(out)}+'/screen-pickers.png'});
 captions.push({file:'screen-pickers.png',caption:'Signal screen pickers; 1440×900 dark.'});
 await page.getByRole('heading',{name:'Looks',exact:true}).evaluate(el=>el.scrollIntoView({block:'start'}));
 await page.waitForTimeout(150);
 await page.screenshot({path:${JSON.stringify(out)}+'/gallery.png'});
 captions.push({file:'gallery.png',caption:'Document ident gallery and Signal screen pickers; 1440×900 dark.'});
 const brands=[['nocturne','Nocturne','late-night'],['meridian','Meridian','cinema'],['blorp','BLORP!','kids'],['neon','NEON 84','retro'],['personal','Your TV','personal'],['new','Untitled look','clean']];
 for(const [id,name,family] of brands){
  await page.goto('http://localhost:5193/ether/livetv/looks?look='+id);
  if(family==='clean')await page.getByRole('button',{name:'Clean preview Clean',exact:true}).click();
  const stage=page.getByRole('slider',{name:'Scrub preview'});await stage.waitFor();
  const pause=page.getByRole('button',{name:'Pause',exact:true});if(await pause.count())await pause.click();
  for(const [moment,label] of [['ident','Ident'],['tonight','Tonight'],['upnext','Up next'],['break','Break'],['back','Back'],[family==='personal'?'welcome':'offair',family==='personal'?'Welcome':'Off air'],['bug','Corner logo'],['lowerthird','Lower third']]){
   await page.getByRole('button',{name:name+' preview '+label,exact:true}).click();
   await stage.evaluate(el=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(el,'5.5');el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));});
   await stage.evaluate(el=>{for(let p=el.parentElement;p;p=p.parentElement)p.scrollTop=0;});
   await page.waitForTimeout(180);
   const file=family+'-'+moment+'.png';await page.screenshot({path:${JSON.stringify(out)}+'/'+file});captions.push({file,caption:name+' · '+label+' · t=5.5 s · document renderer · 1440×900 dark.'});
  }
 }
 return captions;
}`;
try{cli('open','http://localhost:5193/ether/livetv/looks');const r=cli('run-code',code);writeFileSync(out+'/capture.txt',r);const a=r.indexOf('### Result\n'),b=r.indexOf('\n### Ran',a);writeFileSync(out+'/captions.json',JSON.stringify(JSON.parse(r.slice(a+11,b).trim()),null,2)+'\n');}finally{cli('close');}
