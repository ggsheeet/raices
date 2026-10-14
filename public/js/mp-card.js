(function () {
  var mounted = false;
  var mp = null;
  var tokenizing = false;

  function publicKey() {
    var el = document.querySelector("[data-mp-key]");
    if (!el) {
      return "";
    }
    return el.getAttribute("data-mp-key") || "";
  }

  function client() {
    if (mp) {
      return mp;
    }
    var key = publicKey();
    if (!key || typeof MercadoPago !== "function") {
      return null;
    }
    mp = new MercadoPago(key, { locale: "es-MX" });
    return mp;
  }

  function mountFields() {
    if (mounted) {
      return;
    }
    if (
      !document.getElementById("cardNumber") ||
      !document.getElementById("expirationDate") ||
      !document.getElementById("securityCode")
    ) {
      return;
    }
    var sdk = client();
    if (!sdk) {
      return;
    }
    sdk.fields.create("cardNumber", { placeholder: "Número de tarjeta" }).mount("cardNumber");
    sdk.fields.create("expirationDate", { placeholder: "MM/AA" }).mount("expirationDate");
    sdk.fields.create("securityCode", { placeholder: "Código de seguridad" }).mount("securityCode");
    mounted = true;
  }

  function tokenInput(form) {
    return form.querySelector('input[name="token"]');
  }

  function applyToken(form, ctx, id) {
    var input = tokenInput(form);
    if (input) {
      input.value = id;
    }
    var body = ctx && ctx.request && ctx.request.body;
    if (body && typeof body.set === "function") {
      body.set("token", id);
    }
  }

  document.addEventListener("htmx:confirm", function (event) {
    var form = event.target;
    if (!form || form.id !== "pay_form") {
      return;
    }
    event.preventDefault();
    var input = tokenInput(form);
    if (input && input.value) {
      event.detail.issueRequest();
      return;
    }
    if (tokenizing) {
      return;
    }
    var sdk = client();
    if (!sdk) {
      return;
    }
    var nameEl = document.getElementById("card_name");
    var cardholderName = nameEl ? nameEl.value : "";
    tokenizing = true;
    sdk.fields
      .createCardToken({ cardholderName: cardholderName })
      .then(function (result) {
        applyToken(form, event.detail.ctx, result.id);
        event.detail.issueRequest();
      })
      .finally(function () {
        tokenizing = false;
      });
  });

  document.addEventListener("DOMContentLoaded", mountFields);
  document.addEventListener("htmx:afterSwap", mountFields);
  document.addEventListener("htmx:after:swap", mountFields);
  mountFields();
})();
