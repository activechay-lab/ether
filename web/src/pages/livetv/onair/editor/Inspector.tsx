import {Button,Input,Select} from '@braedonsaunders/appkit-ui';
import {t} from '../../../../i18n';
import type {Style,StyleScene,ControlKind} from '../studio-model';
import type {Layer} from '../scene-renderer';
import {findLayer,updateLayer} from './scene-edit';
export function Inspector(p:{style:Style;scene:StyleScene;selected:string[];onChange(style:Style):void}){
 const layer=findLayer(p.scene.document,p.selected[0]??'');if(!layer)return <p className="text-sm text-fg-muted">{t('Select a layer to edit it.')}</p>;
 const patch=(data:Partial<Layer>)=>p.onChange({...p.style,scenes:p.style.scenes.map(s=>s.id===p.scene.id?{...s,document:updateLayer(s.document,layer.id,data)}:s)});
 const field=(key:keyof Layer,label:string,kind:ControlKind='number')=>{const value=layer[key];const input=typeof value==='object'?JSON.stringify(value):String(value??'');return <label key={key} className="block min-w-0 space-y-1 text-xs"><span>{label}</span><Input aria-label={label} type={kind==='number'&&typeof value!=='object'?'number':'text'} value={input} onChange={e=>{const raw=e.target.value;let next:unknown=raw;if(raw.startsWith('{')&&!raw.match(/^\{[\w.]+\}$/)){try{next=JSON.parse(raw)}catch{return}}else if(kind==='number')next=Number(raw);patch({[key]:next})}}/>{kind!=='number'&&<Select aria-label={t('Bind {field}',{field:label})} value="" onChange={e=>{if(e.target.value==='expose'){const id=`${layer.id}-${key}`.replace(/[^a-zA-Z0-9_-]/g,'-');const control={id,label,kind,value:typeof value==='string'?value:input};const document=updateLayer(p.scene.document,layer.id,{[key]:`{control.${id}}`});p.onChange({...p.style,controls:[...p.style.controls.filter(c=>c.id!==id),control],scenes:p.style.scenes.map(s=>s.id===p.scene.id?{...s,document}:s)});}else patch({[key]:`{${e.target.value}}`});}}><option value="" disabled>{t('Bind or expose…')}</option>{['bg','accent','text','glow','type.display','type.body','brand.logo','words.tagline','channel.name','next.title','next.time','next.poster','next.backdrop','now.title','viewer.name','clock',...p.style.controls.map(c=>`control.${c.id}`)].map(token=><option key={token} value={token}>{token}</option>)}<option value="expose">{t('Expose as control…')}</option></Select>}</label>};
 return <section className="space-y-4"><h3 className="font-semibold">{layer.name||layer.id}</h3><div className="grid grid-cols-2 gap-2">{(['x','y','w','h','rotate','scale','scaleX','scaleY','opacity'] as const).map(k=>field(k,t(k)))}</div>
 <div className="space-y-3">{field('fill',t('Fill'),'color')}{field('stroke',t('Stroke'),'color')}{field('strokeWidth',t('Stroke width'))}{(['join','cap','blend'] as const).map(k=>field(k,t(k),'text'))}{field('blur',t('Blur'))}{field('glow',t('Glow'))}
 {layer.type==='text'&&<>{field('text',t('Text'),'text')}{field('font',t('Font'),'font')}{(['size','weight','track','maxWidth','maxLines'] as const).map(k=>field(k,t(k)))}{field('align',t('Alignment'),'text')}{field('fit',t('Fit'),'text')}</>}
 {['image','art','logo'].includes(layer.type)&&<>{field('src',t('Source'),'image')}{field('fit',t('Fit'),'text')}{field('tint',t('Tint'),'color')}</>}
 {layer.type==='path'&&field('path',t('Path'),'text')}
 {['particles','grid','noise'].includes(layer.type)&&<>{field('count',t('Count'))}{field('seed',t('Seed'))}{field('repeat',t('Repeat'))}</>}
 <Button size="sm" variant="ghost" onClick={()=>patch({fill:{linear:[0,0,0,1080],stops:[[0,'{text}'],[.5,'{accent}'],[1,'{bg}']]}})}>{t('Apply gradient')}</Button>
 </div></section>;
}
