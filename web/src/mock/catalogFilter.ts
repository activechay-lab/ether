import type { FakeTitle } from './data';
import { PEOPLE, STUDIOS, KEYWORDS } from './data';

/** Mirror the owner browse wire contract in mock mode, so browser tasks exercise
 * real filtering rather than receiving an unchanged decorative result grid. */
export function matchesCatalogFilter(title: FakeTitle, filter: Record<string, any>): boolean {
  const f = filter;
  const some = (values: string[] | undefined, actual: string[]) => !values?.length || values.some(v => actual.some(a => a.toLowerCase() === v.toLowerCase()));
  if (f.group) {
    const visit = (g: any): boolean => g.filter ? matchesCatalogFilter(title, g.filter) :
      g.unwatched !== undefined ? g.unwatched === (Number(title.key.slice(-2)) % 3 !== 0) :
      g.inProgress !== undefined ? g.inProgress === (Number(title.key.slice(-2)) % 5 === 0) :
      g.operator === 'or' ? g.children.some(visit) : g.children.every(visit);
    if (!visit(f.group)) return false;
  }
  if (f.type && title.type !== f.type) return false;
  if (f.query && !title.title.toLowerCase().includes(f.query.toLowerCase())) return false;
  if (!some(f.genres,title.genres || []) || !some(f.languages,[title.language]) || !some(f.countries,[title.country]) || !some(f.contentRatings,[title.contentRating || ''])) return false;
  for (const [low,high,value] of [
    ['yearFrom','yearTo',title.year || 0],['minRating','maxRating',title.rating || 0],
    ['minVotes','maxVotes',title.votes],['minPopularity','maxPopularity',title.popularity],
    ['minRuntime','maxRuntime',(title.runtimeMinutes || 0)*60e9],
  ] as [string,string,number][]) if ((f[low] && value < f[low]) || (f[high] && value > f[high])) return false;
  if (f.studio && STUDIOS.find(s => s.id === title.studio)?.name !== f.studio) return false;
  if (f.collections?.length && !f.collections.includes(title.collectionId)) return false;
  if (f.keywords?.some((name: string) => !title.keywords.some(id => KEYWORDS.find(k=>k.id===id)?.name.toLowerCase()===name.toLowerCase()))) return false;
  if (f.credits?.some((credit: any) => !(credit.people || []).some((id: number)=>title.people.includes(id)) && !(credit.names || []).some((name: string)=>title.people.some(id=>PEOPLE.find(p=>p.id===id)?.name.toLowerCase()===name.toLowerCase())))) return false;
  if (f.releasedFilters?.some((date: any) => {
    const stamp=Date.parse(title.released)/1000;
    return date.op==='>=' ? stamp<date.unix : date.op==='<=' ? stamp>date.unix+86399 : false;
  })) return false;
  return true;
}
