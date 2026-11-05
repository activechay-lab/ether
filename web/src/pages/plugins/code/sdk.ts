/**
 * A copy of the Ether plugin SDK typings that the editor loads, so
 * completion, hover docs and inline errors come from the real SDK surface.
 */
export const SDK_DTS = `declare module "ether" {
  /** A title in this server's catalogue. */
  export interface Title {
    /** Stable Ether id, for example "movie:603". */
    id: string;
    title: string;
    year?: number;
    kind: "movie" | "show";
    /** 0 to 10, from the catalogue's ratings source. */
    rating?: number;
    genres: string[];
    /** Best playable resolution on this server, for example "2160p". */
    resolution?: "720p" | "1080p" | "2160p";
    /** True for titles rated for children (G, TV-Y, TV-Y7, TV-G) or in Family or Animation. */
    isForKids(): boolean;
  }

  export interface TitleList<T extends Title = Title> extends Array<T> {
    /** Sorts highest first by the given field. */
    sortBy(field: "rating" | "year" | "added" | "popularity"): TitleList<T>;
    filter<S extends T>(predicate: (value: T, index: number, array: T[]) => value is S, thisArg?: any): TitleList<S>;
    filter(predicate: (value: T, index: number, array: T[]) => unknown, thisArg?: any): TitleList<T>;
  }

  export interface CatalogSearch {
    resolution?: "720p" | "1080p" | "2160p";
    /** Lowest rating to include, 0 to 10. */
    minRating?: number;
    genres?: string[];
    kind?: "movie" | "show";
  }

  /** What the plugin may use. Each call is checked against the plugin's grants. */
  export interface Ether {
    catalog: {
      /** Searches this server's catalogue. Never calls the network. */
      search(query: CatalogSearch): Promise<TitleList>;
    };
    viewers: {
      /** Ids of titles that any viewer has started. Needs the "watch history" grant. */
      startedTitles(): Promise<Set<string>>;
    };
    /** Writes a line to the plugin's log, visible in Studio's inspector. */
    log(message: string): void;
  }

  export interface CollectionDef {
    /** Shown as the row and collection name. Use t() so it can be translated. */
    title: string;
    /** How often Ether rebuilds it in the background. */
    refresh: "hourly" | "nightly" | "weekly";
    /** Most titles to keep. */
    limit?: number;
    build(ether: Ether): Promise<Title[]>;
  }

  export interface PluginDef {
    collections?: Record<string, CollectionDef>;
  }

  /** Declares a plugin. Everything it adds is listed here, so Ether can show and check it. */
  export function definePlugin(def: PluginDef): PluginDef;
  /** A translatable string from the plugin's strings file. */
  export function t(text: string): string;
}
`;

/**
 * The global `ether` host that Studio drafts and installed hosted plugins
 * actually run against (internal/plugins/wasm/js.go). Calls are synchronous;
 * each is checked against the plugin's declared permissions.
 */
export const HOST_DTS = `interface EtherFetchRequest {
  url: string;
  method?: string;
  headers?: Record<string, string>;
  body?: string;
  bodyBase64?: string;
}
interface EtherFetchResponse {
  status: number;
  headers: Record<string, string>;
  /** The response text (JSON, HTML, playlists). */
  body: string;
  /** Exact bytes, only for responses that are not text. */
  bodyBase64?: string;
}
interface EtherCatalogTitle {
  id: number;
  title: string;
  type: "movie" | "show";
  year?: number;
  tmdbId?: number;
  imdbId?: string;
}
declare const ether: {
  log(...args: unknown[]): void;
  abort(message?: string): never;
  kv: {
    get(key: string): string | null;
    put(key: string, value: string): void;
    delete(key: string): void;
    list(prefix?: string): string[];
  };
  /** Saved non-secret settings. Apply code defaults for unset values. */
  settings: { get(): Record<string, unknown> };
  secret: { get(name: string): string };
  /** Reaches only hosts listed in the manifest's network permissions. */
  http: { fetch(req: EtherFetchRequest): EtherFetchResponse };
  /** Add-on drafts only: asks the add-on this plugin was made from. */
  addon: { get(route: string): { status: number; body: string } };
  catalog: {
    search(query: string): { titles: EtherCatalogTitle[] };
    get(id: string): EtherCatalogTitle | null;
  };
  viewers: {
    granted(): { id: number; name: string }[];
    history(viewerId: number, page?: number): unknown[];
    setWatchState(v: { viewerId: number; titleId: string; watched: boolean }): void;
  };
  collections: { upsert(collection: Record<string, unknown>): void };
  folders: {
    list(root: string, path?: string): { name: string; path: string; dir: boolean; size?: number }[];
    stat(root: string, path: string): { name: string; path: string; dir: boolean; size?: number };
  };
  notify: { owner(text: string): void };
};
`;
