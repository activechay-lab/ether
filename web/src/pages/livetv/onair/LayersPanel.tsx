import {useState} from 'react';
import {Button,Input} from '@braedonsaunders/appkit-ui';
import {t} from '../../../i18n';
import type {Scene,Layer} from './scene-renderer';
import {updateLayer,moveLayer,duplicateLayers,removeLayers} from './editor/scene-edit';
export function LayersPanel(p:{document:Scene;selected:string[];onSelect(ids:string[]):void;onChange(doc:Scene):void}){
 const [collapsed,setCollapsed]=useState<string[]>([]),[rename,setRename]=useState(''),[menu,setMenu]=useState('');
 const rows=(layers:Layer[],parent?:string,depth=0):React.ReactNode=>[...layers].reverse().map(l=><div key={l.id}>
 <div draggable onDragStart={e=>e.dataTransfer.setData('text/plain',l.id)} onDragOver={e=>e.preventDefault()} onDrop={e=>{e.preventDefault();const id=e.dataTransfer.getData('text/plain');p.onChange(moveLayer(p.document,id,e.shiftKey&&l.type==='group'?l.id:parent,e.shiftKey?l.layers?.length??0:layers.indexOf(l)));}} onContextMenu={e=>{e.preventDefault();setMenu(l.id)}} className={`flex items-center gap-1 rounded p-1 ${p.selected.includes(l.id)?'bg-primary-subtle':'hover:bg-surface-hover'}`} style={{paddingLeft:depth*16+4}}>
 {l.type==='group'&&<Button size="sm" variant="ghost" aria-label={t('Expand group')} onClick={()=>setCollapsed(c=>c.includes(l.id)?c.filter(id=>id!==l.id):[...c,l.id])}>{collapsed.includes(l.id)?'▸':'▾'}</Button>}
 {rename===l.id?<Input autoFocus aria-label={t('Layer name')} value={l.name??l.id} onChange={e=>p.onChange(updateLayer(p.document,l.id,{name:e.target.value}))} onBlur={()=>setRename('')} onKeyDown={e=>{if(e.key==='Enter')setRename('')}}/>:<button className="min-w-0 flex-1 truncate text-left text-sm" onDoubleClick={()=>setRename(l.id)} onClick={e=>p.onSelect(e.shiftKey?[...new Set([...p.selected,l.id])]:[l.id])}>{l.name||t(l.type)}</button>}
 <Button size="sm" variant="ghost" aria-label={t('Toggle visibility')} aria-pressed={!l.hidden} onClick={()=>p.onChange(updateLayer(p.document,l.id,{hidden:!l.hidden}))}>{l.hidden?'○':'●'}</Button><Button size="sm" variant="ghost" aria-label={t('Lock layer')} aria-pressed={!!l.locked} onClick={()=>p.onChange(updateLayer(p.document,l.id,{locked:!l.locked}))}>{l.locked?'▣':'□'}</Button>
 </div>{menu===l.id&&<div className="flex flex-wrap gap-1 p-1"><Button size="sm" variant="ghost" onClick={()=>{setRename(l.id);setMenu('')}}>{t('Rename')}</Button><Button size="sm" variant="ghost" onClick={()=>{p.onChange(duplicateLayers(p.document,[l.id]));setMenu('')}}>{t('Duplicate')}</Button><Button size="sm" variant="ghost" onClick={()=>{p.onChange(removeLayers(p.document,[l.id]));setMenu('')}}>{t('Delete')}</Button></div>}{l.layers&&!collapsed.includes(l.id)&&rows(l.layers,l.id,depth+1)}</div>);
 return <section aria-label={t('Layers')} className="space-y-2"><h3 className="text-sm font-semibold">{t('Layers')}</h3>{rows(p.document.layers)}</section>;
}
