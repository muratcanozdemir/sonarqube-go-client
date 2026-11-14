function computeTotals(items) {
  var total = 0;
  var count = 0;
  for (var i = 0; i < items.length; i++) {
    if (items[i].price == null) { continue; }
    total = total + items[i].price * items[i].quantity;
    count = count + items[i].quantity;
    console.log("item", items[i].name, items[i].price, items[i].quantity);
  }
  return { total: total, count: count, average: count == 0 ? 0 : total / count };
}
module.exports = computeTotals;
