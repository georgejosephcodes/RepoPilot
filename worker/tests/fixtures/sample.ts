import { a } from "b";

const LIMIT = 5;
let notFn = 5;

// comment above
export function exported(x: number): number {
  return x;
}

function plain() {}

export default function () {}

@Component({ selector: "x" })
export class Widget extends Base {
  @Input() name = "";

  constructor() { super(); }

  // doc for method
  method(): void {}

  @HostListener("click")
  onClick() {}

  static create() { return new Widget(); }

  get value() { return 1; }

  handler = () => {};

  plainField = 3;
}

abstract class Abs {
  abstract m(): void;
}

const arrow = (a: number) => a + 1;
export const arrow2 = async () => {};
const fnExpr = function () {};

interface Shape {
  area(): number;
}

export type Id = string | number;

enum Color { Red, Green }

namespace Util {
  export const z = 1;
}
