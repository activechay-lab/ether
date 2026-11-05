// Play every actual encoded master in a batch, capture four moments and record
// browser decode telemetry. Review the screenshots before reporting the batch.
import {execFileSync} from 'node:child_process';
import {mkdirSync,writeFileSync} from 'node:fs';
const [batch='late-night',output='/tmp/onair-watch']=process.argv.slice(2);mkdirSync(output,{recursive:true});
const files=batch==='overlay'?['late-night-overlay','late-night-overlay-720p']:[batch+'-ident',batch+'-ident-720p',batch+'-upnext',batch+'-upnext-720p',batch+'-break',batch+'-break-720p'];
const session='w6-watch-'+batch;
const cli=(...args)=>execFileSync('playwright-cli',['-s='+session,...args],{encoding:'utf8',env:{...process.env,PLAYWRIGHT_SKIP_BROWSER_GC:'1'},maxBuffer:8e6});
try{
 cli('open','http://localhost:5195');
 const outputs=[];
 for(const current of files){
 const code=`async page=>{
  await page.setViewportSize({width:1440,height:900});
  await page.setContent('<style>body{background:#101827;color:#efe5cb;margin:20px;font:18px system-ui}video{width:1280px;height:720px;display:block}</style><h1></h1><video controls></video><p></p>');
  const results=[];const video=page.locator('video');
  for(const file of ${JSON.stringify(["${CURRENT}"])}){
   await page.locator('h1').textContent();
   await page.evaluate(async file=>{document.querySelector('h1').textContent=file;document.querySelector('p').textContent=(await(await fetch('http://localhost:5195/'+file+'.json')).json()).caption;const v=document.querySelector('video');v.src='http://localhost:5195/'+file+'.mp4';},file);
   await video.evaluate(v=>new Promise((resolve,reject)=>{v.onloadeddata=()=>resolve();v.onerror=()=>reject(new Error('video load '+v.src));if(v.readyState>=2)resolve();}));
   await video.evaluate(v=>v.play());
   for(let i=0;i<4;i++){await page.waitForTimeout(4000);await page.screenshot({path:${JSON.stringify(output)}+'/'+file+'-'+i+'.png'});}
   results.push(await video.evaluate(v=>({file:v.src,time:v.currentTime,ended:v.ended,width:v.videoWidth,height:v.videoHeight,error:v.error?.message,totalFrames:v.getVideoPlaybackQuality().totalVideoFrames,droppedFrames:v.getVideoPlaybackQuality().droppedVideoFrames})));
  }
  return results;
 }`;
 const result=cli('run-code',code.replaceAll("${CURRENT}",current));outputs.push(result);writeFileSync(output+'/playback.txt',outputs.join("\n"));
 }
}finally{cli('close');}
