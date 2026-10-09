// Keep enum hints in read-only examples; executable samples and the spec stay valid.
function SwaggerEnumHintsPlugin(system) {
  const React = system.React;
  const plain = value => value && typeof value.toJS === "function" ? value.toJS() : value;
  const label = value => value === "" ? '""' : String(value);

  function hint(value, schema, root, depth) {
    if (!schema || depth > 100 || Object.prototype.hasOwnProperty.call(schema, "example") ||
        Object.prototype.hasOwnProperty.call(schema, "default")) return value;
    if (schema.$ref && schema.$ref.startsWith("#/")) {
      const target = schema.$ref.slice(2).split("/").reduce((node, key) =>
        node && node[key.replace(/~1/g, "/").replace(/~0/g, "~")], root);
      value = hint(value, target, root, depth + 1);
    }
    for (const part of schema.allOf || []) value = hint(value, part, root, depth + 1);
    if (Array.isArray(schema.enum) && schema.enum.length &&
        schema.enum.every(item => item === null || typeof item === "string")) {
      return schema.enum.map(label).join("|");
    }
    if (Array.isArray(value)) return value.map(item => hint(item, schema.items, root, depth + 1));
    if (value && typeof value === "object") {
      return Object.fromEntries(Object.entries(value).map(([key, item]) =>
        [key, hint(item, (schema.properties || {})[key] || schema.additionalProperties, root, depth + 1)]));
    }
    return value;
  }

  return {
    wrapComponents: {
      modelExample: Original => props => {
        if (!props.isExecute && React.isValidElement(props.example) &&
            typeof props.example.props.children === "string") {
          try {
            const value = JSON.parse(props.example.props.children);
            const result = hint(value, plain(props.schema), plain(system.specSelectors.specJson()), 0);
            props = {...props, example: React.cloneElement(props.example, {}, JSON.stringify(result, null, 2))};
          } catch (_) { /* Non-JSON examples use Swagger UI's original rendering. */ }
        }
        return React.createElement(Original, props);
      },
      EnumModel: Original => props => React.createElement(Original, {
        ...props, value: props.value.map(value => value === "" ? '""' : value)
      })
    }
  };
}
