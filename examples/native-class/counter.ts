export class Counter {
  count: number;
  name: string;

  constructor(name: string) {
    this.name = name;
    this.count = 0;
  }

  increment(): number {
    this.count += 1;
    return this.count;
  }

  decrement(): number {
    this.count -= 1;
    return this.count;
  }

  describe(): string {
    return this.name + ": " + this.count;
  }
}
