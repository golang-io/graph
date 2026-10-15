/**
 * @golang-io/graph — Graph canvas kernel (xyflow + X6).
 * Host pages must not import @xyflow/react or @antv/x6 directly.
 */
export * from './node-pack'
export * from './node-spec'
export * from './endpoints'
export * from './useGraphNodeCatalog'
export * from './core-pack'
export * from './types'
export * from './run-overlay'
export { GraphI18nProvider, useGraphI18n, type GraphTranslate } from './i18n'

export {
  graphEditorCanvasConfig,
  graphRunCanvasConfig,
  type GraphCanvasConfig,
  type GraphEntryConfig,
  type GraphStructureMode,
} from './entry-config'
export {
  catalogKind,
  createNodeRegistry,
  emptyNodeRegistry,
  INNER_NODE_PREFIX,
  resolveStepAppearance,
  type NodeTypeDef,
  type NodeTypeRegistry,
  type GraphNodeRole,
} from './node-registry'
export { GraphCanvas, type GraphCanvasProps } from './GraphCanvas'
export {
  GraphNodePropsPanel,
  type GraphNodePropsPanelProps,
  type GraphNodePropsValue,
} from './GraphNodePropsPanel'
export { GraphWorkbench, type GraphWorkbenchProps } from './GraphWorkbench'
export { GraphPaletteGroup, type GraphPaletteGroupProps } from './GraphPaletteGroup'
export { GraphPalette, type GraphPaletteProps } from './GraphPalette'
export { GraphEditToolbar, type GraphEditToolbarProps } from './GraphEditToolbar'
export { Select, type SelectOption, type SelectProps } from './host-ui'
export {
  mergeNodeLibraries,
  groupLibraryItems,
  libraryDefaultConfig,
  libraryItemLabel,
  type NodeLibrary,
  type NodeLibraryItem,
} from './node-library'
export { annotationLibrary } from './core-pack'
export {
  GraphNodeRegistryProvider,
  useGraphNodeRegistry,
} from './GraphNodeRegistryContext'
export { parseGraphUI } from './graph-adapter'
export * from './document'
export { insertionPosition, insertionScope, scopeGraphOf, scopedId, scopeOf, setBodyCollapsed } from './body-scope'
export { useGraphDocument } from './useGraphDocument'
export { layoutGraph } from './layout'
export { NODE_SIZE, instanceEndpointIds } from './node-size'
export {
  CanvasRendererBadge,
  useCanvasRenderer,
  type CanvasRenderer,
} from './canvas-renderer'
export { applyAlignToNodes, type AlignOp } from './align-selection'
export {
  ANNOTATION_COLOR_PRESETS,
  ANNOTATION_DEFAULT_COLOR,
  ANNOTATION_DEFAULT_SIZE,
  normalizeAnnotationColor,
  parseAnnotationColor,
} from './annotation-style'
export { isHandleBrandedWhen } from './edge-route'
export { edgeWhenLabel } from './switch-config'
export { isGraphDarkMode } from './edge-style'
export {
  DEFAULT_ERROR_SIDE,
  DEFAULT_INPUT_SIDE,
  DEFAULT_OUTPUT_SIDE,
  SIDES,
  SIDE_LABEL,
  endpointIds,
  endpointSide,
  parseEndpointSides,
  resolveEndpointSides,
  rotateEndpointSides,
  setEndpointSide,
  type NodeEndpointIds,
  type EndpointSides,
  type Side,
} from './orient'
export { NodeInputsFields } from './NodeInputsFields'
export { GraphInputsFields } from './GraphInputsFields'
export {
  runInputFieldsFromSchema,
  runInputNames,
  runInputSchemaFromFields,
  type RunInputRow,
} from './run-input-schema'
export {
  GRAPH_ERROR_FIELD,
  ENDPOINT_PAYLOAD_DEFAULT,
  allOutputFields,
  fieldEndpoint,
  inputSourcesFor,
  isValidDataName,
  nodeDeclaredOutput,
  schemaPropertyNames,
  type DeclaredOutput,
  type GraphInputSourceOption,
} from './node-outputs'
export { SwitchConfigFields, type SwitchCase } from './SwitchConfigFields'
export { buildRunStatusMaps, type RunStatus } from './run-status'
export { ListRowRemoveButton } from './ListRowRemoveButton'
export {
  useGraphHistory,
  createHistoryEntry,
  describeGraphDiff,
  sameGraphSnapshot,
  loadStoredGraphHistory,
  saveStoredGraphHistory,
  clearStoredGraphHistory,
  type GraphHistoryEntry,
  type GraphHistoryOptions,
  type GraphHistoryPushOptions,
  type StoredGraphHistory,
} from './useGraphHistory'
export {
  GRAPH_BUNDLE_KIND,
  buildGraphBundle,
  parseGraphBundle,
  downloadGraphBundle,
  slugGraphName,
  readFileAsText,
  bundleErrorI18nKey,
  type GraphBundle,
} from './graph-bundle'

