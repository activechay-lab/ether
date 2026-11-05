import { layerGeometry, type Scene, type SceneBindings } from '../scene-renderer';
export function layerBounds(doc: Scene,b: SceneBindings,t: number) { return layerGeometry(doc,b,t); }
