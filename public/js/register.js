function money(amount) {
  return "Mex$" + amount.toLocaleString("es-MX", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
}

document.addEventListener("alpine:init", function () {
  Alpine.data("ticket", function () {
    return {
      qty: 1,
      price: 0,
      min: 0,
      max: 10,
      speaker: "",
      init: function () {
        var unitRaw = this.$el.getAttribute("data-unit-pesos");
        if (unitRaw !== null) {
          this.price = Number(unitRaw);
        }
        var raw = this.$el.getAttribute("data-qty");
        if (!raw) {
          return;
        }
        var n = Number(raw);
        if (Number.isInteger(n) && n >= 1 && n <= this.max) {
          this.qty = n;
        }
      },
      get atMin() {
        return this.qty <= this.min;
      },
      get atMax() {
        return this.qty >= this.max;
      },
      get lineQty() {
        return this.qty + " x Admisión general";
      },
      get lineTotal() {
        return money(this.qty * this.price);
      },
      get subtotal() {
        return money(this.qty * this.price);
      },
      get total() {
        return money(this.qty * this.price);
      },
      increment: function () {
        if (this.qty < this.max) {
          this.qty += 1;
        }
      },
      decrement: function () {
        if (this.qty > this.min) {
          this.qty -= 1;
        }
      },
      showSpeaker: function (id) {
        this.speaker = id;
      },
      closeSpeaker: function () {
        this.speaker = "";
      },
    };
  });
});
