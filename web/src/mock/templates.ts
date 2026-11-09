import type { CollectionTemplate, CollectionTemplateBundle } from "../lib/contract";
import fixture from "./templates.json";

// Generated from the server catalogue by TestMockTemplatesMatchGo -update.
// Keep canonical source copy here; the UI translates it at render time.
export const TEMPLATES = fixture.templates as CollectionTemplate[];
export const TEMPLATE_BUNDLES = fixture.bundles as CollectionTemplateBundle[];
