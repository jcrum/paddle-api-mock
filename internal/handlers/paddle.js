// Development stand-in for Paddle.js, served by the Paddle mock. Checkout.open completes a checkout at once
// through the mock's /admin/checkout (no card form) and then behaves as Paddle.js does on success: it reports
// checkout.completed to the eventCallback and follows settings.successUrl.
(function () {
  var origin = "__MOCK_ORIGIN__";
  var state = { eventCallback: null };

  function emit(name, data) {
    if (typeof state.eventCallback === "function") state.eventCallback({ name: name, data: data });
  }

  function open(options) {
    options = options || {};
    var items = (options.items || []).map(function (item) {
      return { price_id: item.priceId, quantity: item.quantity || 1 };
    });
    var body = { items: items, email: (options.customer || {}).email, custom_data: options.customData || null };
    emit("checkout.loaded", {});
    return fetch(origin + "/admin/checkout", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    })
      .then(function (response) {
        return response.json().then(function (json) { return { ok: response.ok, json: json }; });
      })
      .then(function (result) {
        if (!result.ok) {
          emit("checkout.error", result.json.error || {});
          return;
        }
        var transaction = result.json.data.transaction;
        emit("checkout.completed", { transaction_id: transaction.id, status: transaction.status, custom_data: transaction.custom_data });
        var successUrl = (options.settings || {}).successUrl;
        if (successUrl) window.location.assign(successUrl);
      })
      .catch(function (error) {
        emit("checkout.error", { detail: String(error) });
      });
  }

  window.Paddle = {
    Environment: { set: function () {} },
    Initialize: function (options) {
      state.eventCallback = (options || {}).eventCallback || null;
    },
    Checkout: { open: open, close: function () {} }
  };
})();
