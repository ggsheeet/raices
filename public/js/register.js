function money(amount) {
  return "Mex$" + amount.toLocaleString("es-MX", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
}

var focusGivenName = false;

document.addEventListener("htmx:after:settle", function (event) {
  if (!event.target || event.target.id !== "payer_form") {
    return;
  }
  focusGivenName = true;
  var reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  event.target.scrollIntoView({
    block: "start",
    behavior: reduceMotion ? "auto" : "smooth",
  });
});

document.addEventListener("htmx:after:swap", function () {
  if (!focusGivenName) {
    return;
  }
  focusGivenName = false;
  var input = document.getElementById("given_name");
  if (!input) {
    return;
  }
  input.focus({ preventScroll: true });
});

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
