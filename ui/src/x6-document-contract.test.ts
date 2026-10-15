import { describe, expect, it } from 'vitest'
import { editorNodeToX6, editorEdgeToX6, isX6NodePayloadChanged } from './x6-adapter'
import { createGraphNodePackFromCatalog } from './node-spec'
import type { GraphEditorNode } from './document'

const switchOutputs = Array.from({ length: 16 }, (_, i) => `Case${i}`)
const pack = createGraphNodePackFromCatalog('test', [
  {ID: 'biz', Name: 'Biz',Endpoints:{Inputs:['In'],Outputs:['Out']}},
  {ID: 'stop', Name: 'Stop',Endpoints:{Inputs:['In'],Outputs:['Error']}},
  {ID: 'call', Name: 'Call',Endpoints:{Inputs:['In'],Outputs:['Out','Error']}},
  {ID: '__INNER__.switch', Name: 'Branch',Endpoints:{Inputs:['In'],Outputs: switchOutputs}},
])
const node: GraphEditorNode = {id:'n',type:'biz',name:'Biz',config:{URL:'https://example.com'},position:{x:0,y:0}}
const stopNode: GraphEditorNode = {id:'s',type:'stop',name:'Stop',config:{},position:{x:0,y:0}}
const items = (n: GraphEditorNode) => (editorNodeToX6(n, pack.registry).ports as {items:{id:string;group:string}[]}).items

describe('X6 author metadata', () => {
  it('fills missing endpoint sides: a lone Error output goes right, Error beside other outputs goes bottom', () => {
    const ports = items(stopNode)
    // 只有 Error 一个出口时按普通出口补齐（右边）
    expect(ports).toEqual([{id:'In',group:'in'},{id:'Error',group:'error-right'}])
    expect(new Set(ports.map((p) => p.id)).size).toBe(ports.length)
    expect(items(node).map((p) => p.id)).toEqual(['In','Out'])
    // 另有 Out 时缺失的 Error 放下边
    expect(items({...node,type:'call'})).toEqual([{id:'In',group:'in'},{id:'Out',group:'out'},{id:'Error',group:'error-bottom'}])
    expect(editorNodeToX6(node, pack.registry).data.endpointSides).toEqual({In:'left',Out:'right'})
  })
  it('places each endpoint on its own side and never shares an input side', () => {
    const groups = (n: GraphEditorNode) => (editorNodeToX6(n, pack.registry).ports as {groups: Record<string, {position: string}>; items: {id: string; group: string}[]})
    const tb = groups({...node,type:'call',endpointSides:{In:'top',Out:'bottom',Error:'left'}})
    expect(tb.groups.in.position).toBe('top')
    expect(tb.groups.out.position).toBe('bottom')
    expect(tb.items.find((p) => p.id === 'Error')).toEqual({id:'Error',group:'error-left'})
    // Error 写成与入口同边（top）时顺时针移到 right
    const clash = groups({...stopNode,endpointSides:{In:'top',Error:'top'}})
    expect(clash.items.find((p) => p.id === 'Error')).toEqual({id:'Error',group:'error-right'})
    expect(clash.groups['error-right'].position).toBe('right')
  })
  it('creates switch ports only for configured branches', () => {
    const branch = {...node,type:'__INNER__.switch',config:{Cases:[{ID:'Case0',Expr:'true'},{ID:'Case4',Expr:'false'},{ID:'Case1',IsDefault:true}]}}
    expect(items(branch).map((p) => p.id)).toEqual(['In', 'Case0', 'Case4', 'Case1'])
    expect(items({...branch,config:{Expr:'score >= 60'}}).map((p) => p.id)).toEqual(['In', 'Case0', 'Case1'])
  })
  it('maps a changed edge without requiring a new identity', () => {
    const edge = {id:'e',from:'n',to:'end',when:'Case0'}
    const next = editorEdgeToX6({...edge,to:'other',when:'Case1',label:'ELSE'})
    expect(next).toMatchObject({id:'e',source:{cell:'n',port:'Case1'},target:{cell:'other',port:'In'}})
    expect(next.labels).toMatchObject([{attrs:{text:{text:'ELSE'}}}])
  })
  it('keeps author dimensions and fractional coordinates', () => {
    expect(editorNodeToX6({...node,type:'annotation',position:{x:1.25,y:2.5},width:300,height:150})).toMatchObject({x:1.25,y:2.5,width:300,height:150})
  })
  it('does not recreate HTML for a cloned unchanged config during view updates', () => {
    expect(isX6NodePayloadChanged(node,{...node,config:structuredClone(node.config),selected:true,runStatus:'running'})).toBe(false)
    expect(isX6NodePayloadChanged(node,{...node,config:{URL:'https://other.example'}})).toBe(true)
  })
})
