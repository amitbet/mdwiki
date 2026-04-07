/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_SPACE_KEY: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
