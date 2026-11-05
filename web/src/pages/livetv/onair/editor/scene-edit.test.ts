import {describe,it,expect} from 'vitest';
import {groupLayers,ungroup,moveLayer,duplicateLayers,findLayer} from './scene-edit';
import type {Scene} from '../scene-renderer';
const scene:Scene={v:1,moment:'ident',duration:8,poster:3,layers:[{id:'a',type:'text'},{id:'b',type:'path'},{id:'c',type:'rect'}]};
describe('document operations',()=>{
it('groups and ungroups without changing order or input',()=>{const grouped=groupLayers(scene,['a','b']);expect(grouped.layers[0]?.layers?.map(l=>l.id)).toEqual(['a','b']);expect(ungroup(grouped,grouped.layers[0]!.id).layers.map(l=>l.id)).toEqual(['a','b','c']);expect(scene.layers).toHaveLength(3)});
it('rejects cyclic moves and allocates every duplicate id',()=>{const g=groupLayers(scene,['a','b']);expect(moveLayer(g,g.layers[0]!.id,'a',0)).toBe(g);const d=duplicateLayers(g,[g.layers[0]!.id]);const ids:string[]=[];const walk=(ls:Scene['layers'])=>ls.forEach(l=>{ids.push(l.id);if(l.layers)walk(l.layers)});walk(d.layers);expect(new Set(ids).size).toBe(ids.length);expect(findLayer(d,'a')).toBeDefined()});
});
