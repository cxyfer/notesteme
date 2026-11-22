// web/shared 的入口：Client 用 `@shared` 別名匯入（web/client/vite.config.ts、tsconfig.json）。
export {
  StatusBand,
  tickInterval,
  type BandSegment,
  type BandStatus,
  type StatusBandProps,
  type StatusBandStage,
} from "./StatusBand";
export { diffChars, hasChanges, type DiffPart } from "./diff";
export { DiffText, type DiffTextProps } from "./DiffText";
