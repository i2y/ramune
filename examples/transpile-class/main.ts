// Class inheritance example

class Animal {
  name: string;
  private sound: string;

  constructor(name: string, sound: string) {
    this.name = name;
    this.sound = sound;
  }

  speak(): string {
    return `${this.name} says ${this.sound}`;
  }
}

class Dog extends Animal {
  breed: string;

  constructor(name: string, breed: string) {
    super(name, "Woof!");
    this.breed = breed;
  }

  describe(): string {
    return `${this.name} (${this.breed})`;
  }
}

class Cat extends Animal {
  indoor: boolean;

  constructor(name: string, indoor: boolean) {
    super(name, "Meow!");
    this.indoor = indoor;
  }
}

// Create animals
const dog = new Dog("Rex", "Labrador");
const cat = new Cat("Whiskers", true);

console.log(dog.speak());
console.log(dog.describe());
console.log(cat.speak());
console.log("Indoor cat:", cat.indoor);

// Enum example
enum Color {
  Red = "red",
  Green = "green",
  Blue = "blue",
}

function printColor(c: Color): void {
  console.log("Color:", c);
}

printColor(Color.Red);
printColor(Color.Blue);
