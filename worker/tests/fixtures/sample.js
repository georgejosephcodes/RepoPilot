const x = require("x");
const y = require("y");
const z = require("z");

// comment above
function top() {}

export function exported() {}

class Foo extends Bar {
  constructor() { super(); }
  method() {}
  static s() {}
  #priv() {}
  get v() { return 1; }
  handler = () => {};
}

const arrow = () => 1;
var old = function named() {};
exports.helper = function () {};
Foo.prototype.bar = function () {};
module.exports = { top };
