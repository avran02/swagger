const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const context = vm.createContext({});
vm.runInContext(fs.readFileSync(path.join(__dirname, '../ui_enum.js'), 'utf8'), context);
const schema = {type:'object', properties:{
  resource_type:{type:'string', enum:['article','test','course','none','',null]},
  list:{type:'array', items:{$ref:'#/components/schemas/Status'}},
  map:{type:'object', additionalProperties:{$ref:'#/components/schemas/Status'}},
  explicit:{type:'string',enum:['a','b'],example:'a'},
  defaulted:{type:'string',enum:['a','b'],default:'b'},
  number:{type:'integer',enum:[1,2]}
}};
const root = {components:{schemas:{Status:{type:'string',enum:['a','b']}}}};
const React = {
  isValidElement: element => Boolean(element && element.props),
  createElement: (type, props) => ({type,props}),
  cloneElement: (element, props, children) => ({...element,props:{...element.props,...props,children}})
};
const plugin = context.SwaggerEnumHintsPlugin({React,specSelectors:{specJson:()=>({toJS:()=>root})}});
const render = plugin.wrapComponents.modelExample('original');
const props = {schema:{toJS:()=>schema},example:{props:{children:JSON.stringify({resource_type:'article',list:['a'],map:{key:'a'},explicit:'a',defaulted:'b',number:1})}}};

test('read-only string enum hints include every value and explicitly show empty/null', () => {
  const result = JSON.parse(render(props).props.example.props.children);
  assert.equal(result.resource_type, 'article|test|course|none|""|null');
  assert.deepEqual(result.list,['a|b']);
  assert.deepEqual(result.map,{key:'a|b'});
  assert.equal(result.explicit,'a');
  assert.equal(result.defaulted,'b');
  assert.equal(result.number,1);
  assert.equal(JSON.parse(props.example.props.children).resource_type,'article');
  assert.deepEqual(schema.properties.resource_type.enum,['article','test','course','none','',null]);
});
test('Try it out keeps the original executable example', () => {
  assert.equal(render({...props,isExecute:true}).props.example,props.example);
});
test('non-JSON examples are preserved', () => {
  const example={props:{children:'<xml/>'}};
  assert.equal(render({...props,example}).props.example,example);
});
test('empty enum entries are visible without modifying the original values', () => {
  const value=['a','',null];
  const result=plugin.wrapComponents.EnumModel('enum')({value});
  assert.deepEqual(Array.from(result.props.value),['a','""',null]);
  assert.deepEqual(value,['a','',null]);
});
