const { fetchData, add } = require('native:async');

console.log("Sync:", add(3, 4));

// Async function returns a Promise
const p = fetchData("World");
p.then(function(result) {
  console.log("Async:", result);
});
