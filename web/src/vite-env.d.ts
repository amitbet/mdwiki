/// <reference types="vite/client" />

declare module "plantuml-encoder";

declare module "turndown-plugin-gfm" {
  export function gfm(service: unknown): void;
  export function tables(service: unknown): void;
  export function strikethrough(service: unknown): void;
}
