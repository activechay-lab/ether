// Run while mock Vite is on :5193. Uses the installed host playwright-cli;
// no browser install or native image dependency. Output is a JSON report.
import { execFileSync } from 'node:child_process';
import { writeFileSync, mkdirSync } from 'node:fs';
const output = process.argv[2] || '/tmp/onair-w6';
mkdirSync(output, { recursive: true });
const session = `onair-acceptance-${process.pid}`;
const cli = (...args) => execFileSync('playwright-cli', [`-s=${session}`, ...args], { encoding: 'utf8', env: { ...process.env, PLAYWRIGHT_SKIP_BROWSER_GC: '1' }, maxBuffer: 20 * 1024 * 1024 });
try {
  cli('open', 'http://localhost:5193/ether/livetv/looks');
  const code = `async page => {
    return await page.evaluate(async () => {
      const renderer = await import('/ether/src/pages/livetv/onair/scene-renderer.ts');
      await renderer.loadOnAirFonts();
      const entries = await (await fetch('/ether/@fs${process.cwd()}/internal/onair/raster/testdata/manifest.json')).json();
      const canvas = document.createElement('canvas'); canvas.width = 480; canvas.height = 270;
      const ctx = canvas.getContext('2d', {willReadFrequently:true});
      const scores = [];
      for (const entry of entries) {
        const {family, moment, bindings} = entry; const doc = renderer.documentFor(family,moment);
        for (const time of family === 'signal' ? [1.5] : [.5,3,5.5]) {
          renderer.renderScene(ctx,doc,bindings,time);
          const actual = ctx.getImageData(0,0,480,270).data;
          const url = '/ether/@fs${process.cwd()}/internal/onair/raster/testdata/' + family + '-' + doc.moment + '-' + time.toFixed(1) + '.png';
          const image = await createImageBitmap(await (await fetch(url)).blob());
          ctx.clearRect(0,0,480,270);ctx.drawImage(image,0,0);image.close();
          const expected = ctx.getImageData(0,0,480,270).data;
          let error=0;
          for(let i=0;i<actual.length;i+=4) error += (.2126*Math.abs(actual[i]-expected[i])+.7152*Math.abs(actual[i+1]-expected[i+1])+.0722*Math.abs(actual[i+2]-expected[i+2]))/255;
          scores.push({family,moment:doc.moment,time,error:error/(480*270)});
        }
      }
      canvas.width=960;canvas.height=540;
      const performance=[];
      for(const entry of entries) {
        for(let i=0;i<3;i++)renderer.renderScene(ctx,renderer.documentFor(entry.family,entry.moment),entry.bindings,i*.1);
        const durations=[]; const wallStart=globalThis.performance.now();
        for(let i=0;i<30;i++) { const start=globalThis.performance.now(); renderer.renderScene(ctx,renderer.documentFor(entry.family,entry.moment),entry.bindings,.5+i*.17); durations.push(globalThis.performance.now()-start); await new Promise(requestAnimationFrame); }
        const meanMs=durations.reduce((a,b)=>a+b,0)/durations.length;
        performance.push({family:entry.family,moment:entry.moment,meanMs,renderFps:1000/meanMs,fps:30*1000/(globalThis.performance.now()-wallStart)});
      }
      return {scores,performance,tolerance:.02,parityPass:scores.every(s=>s.error<=.02),performancePass:performance.every(s=>s.fps>=45)};
    });
  }`;
  const result = cli('run-code', code);
  writeFileSync(`${output}/browser-result.txt`, result);
  const begin = result.indexOf('### Result\n');
  const end = result.indexOf('\n### Ran', begin);
  const json = JSON.parse(result.slice(begin + '### Result\n'.length, end).trim());
  writeFileSync(`${output}/acceptance.json`, JSON.stringify(json,null,2)+'\n');
  console.log(JSON.stringify(json));
  if (!json.parityPass || !json.performancePass) process.exitCode=1;
} finally { cli('close'); }
