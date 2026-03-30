// Getter/setter example

class Temperature {
  private _celsius: number;

  constructor(celsius: number) {
    this._celsius = celsius;
  }

  get celsius(): number {
    return this._celsius;
  }

  set celsius(value: number) {
    this._celsius = value;
  }

  get fahrenheit(): number {
    return this._celsius * 9 / 5 + 32;
  }

  set fahrenheit(value: number) {
    this._celsius = (value - 32) * 5 / 9;
  }

  describe(): string {
    return this._celsius + "°C / " + this.fahrenheit + "°F";
  }
}

class Person {
  private _firstName: string;
  private _lastName: string;
  private _age: number;

  constructor(first: string, last: string, age: number) {
    this._firstName = first;
    this._lastName = last;
    this._age = age;
  }

  get fullName(): string {
    return this._firstName + " " + this._lastName;
  }

  get age(): number {
    return this._age;
  }

  set age(value: number) {
    if (value >= 0) {
      this._age = value;
    }
  }
}

// Temperature tests
console.log("Temperature tests:");
const temp = new Temperature(100);
console.log("  " + temp.describe());

temp.celsius = 0;
console.log("  after set 0°C: " + temp.describe());

temp.fahrenheit = 212;
console.log("  after set 212°F: " + temp.describe());

// Person tests
console.log("\nPerson tests:");
const person = new Person("John", "Doe", 30);
console.log("  name:", person.fullName);
console.log("  age:", person.age);

person.age = 31;
console.log("  after birthday:", person.age);
