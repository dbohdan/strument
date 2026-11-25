#!/usr/bin/env python3
"""bughunt.py: the preserved-reasoning trial's fixture, a shop library with six bugs.

    bughunt.py build DIR      write the buggy project (shop/, tests/, SPEC.md) to DIR
    bughunt.py reference DIR  write the fixed project to DIR
    bughunt.py hidden DIR     write the hidden suite to DIR (run against a project's shop/)
    bughunt.py check          build both in a temp dir and verify every claim below

Claims check verifies: the reference passes the visible and hidden suites; the
buggy project fails the visible suite; each bug alone, put into the reference,
fails at least one visible test and one hidden test. "Alone" is the state after
the other five are fixed, so no bug is undetectable or hidden behind another.
"""
import os, shutil, subprocess, sys, tempfile

SPEC = r'''# shop

A small library that prices a shopping cart. Amounts are integer cents
throughout; `money.parse` and `money.fmt` convert to and from text.

## Money

`parse(text)` reads "12", "12.5" or "12.50" as 1200, 1250, 1250 cents.
`fmt(cents)` writes 1250 as "12.50". `percent(cents, rate)` is cents * rate /
100, rounded half up to a whole cent (2.5 cents rounds to 3).

## Catalog

`load(text)` reads CSV lines `sku,name,category,price,weight_g` (no header).

## Cart

`Cart.add(sku, qty)` adds units. Adding a SKU already in the cart adds to its
quantity. `lines()` returns `(sku, qty)` pairs sorted by SKU.

## Discounts

Applied per line, in this order:

1. **Category percent off.** `category_off = {"books": 10}` takes 10% off each
   unit price in that category, via `money.percent`, before multiplying by the
   quantity.
2. **Multi-buy.** `multibuy = {"SKU": (x, y)}` means buy x, get y free: of each
   x + y units, y are free. 7 units of a (2, 1) offer pay for 5.

A coupon `(amount, min_spend)` takes `amount` off the order when the sum of the
discounted line nets is at least `min_spend`. It never takes the order below
zero.

## Tax

Each line's discounted net is taxed at its category's rate: books and food 7%,
everything else 19%. The tax is computed per line with `money.percent`, so
each line rounds half up on its own.

## Shipping

By total cart weight in grams: up to and including 500 g costs 4.90, up to and
including 2000 g costs 7.90, heavier costs 12.90. Shipping is free when the
goods total (discounted nets plus tax, minus the coupon) is 50.00 or more.

## Invoice

`invoice.total(...)` is the discounted nets plus tax, minus the coupon, plus
shipping. `invoice.render(...)` prints one row per line and the totals.
'''

FILES = {
"shop/__init__.py": "",
"shop/money.py": r'''def parse(text):
    """Text such as "12", "12.5" or "12.50" to integer cents."""
    text = text.strip()
    if "." in text:
        whole, frac = text.split(".", 1)
        frac = (frac + "00")[:2]
        return int(whole) * 100 + int(frac)
    return int(text) * 100


def fmt(cents):
    sign = "-" if cents < 0 else ""
    cents = abs(cents)
    return f"{sign}{cents // 100}.{cents % 100:02d}"


def percent(cents, rate):
    """cents * rate / 100, rounded half up to a whole cent."""
    return (cents * rate * 2 + 100) // 200
''',
"shop/catalog.py": r'''from shop import money


class Item:
    def __init__(self, sku, name, category, price, weight_g):
        self.sku, self.name, self.category = sku, name, category
        self.price, self.weight_g = price, weight_g


def load(text):
    items = {}
    for line in text.strip().splitlines():
        sku, name, category, price, weight = [f.strip() for f in line.split(",")]
        items[sku] = Item(sku, name, category, money.parse(price), int(weight))
    return items
''',
"shop/cart.py": r'''class Cart:
    def __init__(self):
        self._qty = {}

    def add(self, sku, qty=1):
        self._qty[sku] = self._qty.get(sku, 0) + qty

    def lines(self):
        return sorted(self._qty.items())
''',
"shop/discounts.py": r'''from shop import money


def line_net(item, qty, category_off=None, multibuy=None):
    """A line's net after the category discount and any multi-buy offer."""
    unit = item.price
    off = (category_off or {}).get(item.category)
    if off:
        unit -= money.percent(unit, off)
    offer = (multibuy or {}).get(item.sku)
    paid = qty
    if offer:
        x, y = offer
        paid = qty - (qty // (x + y)) * y
    return unit * paid


def coupon_value(coupon, discounted_net):
    if not coupon:
        return 0
    amount, min_spend = coupon
    if discounted_net < min_spend:
        return 0
    return min(amount, discounted_net)
''',
"shop/tax.py": r'''from shop import money

RATES = {"books": 7, "food": 7}
DEFAULT_RATE = 19


def line_tax(category, net):
    return money.percent(net, RATES.get(category, DEFAULT_RATE))
''',
"shop/shipping.py": r'''FREE_FROM = 5000


def cost(weight_g, goods_total):
    if goods_total >= FREE_FROM:
        return 0
    if weight_g <= 500:
        return 490
    if weight_g <= 2000:
        return 790
    return 1290
''',
"shop/invoice.py": r'''from shop import discounts, money, shipping, tax


def lines(cart, catalog, category_off=None, multibuy=None):
    out = []
    for sku, qty in cart.lines():
        item = catalog[sku]
        net = discounts.line_net(item, qty, category_off, multibuy)
        out.append((item, qty, net, tax.line_tax(item.category, net)))
    return out


def totals(cart, catalog, category_off=None, multibuy=None, coupon=None):
    rows = lines(cart, catalog, category_off, multibuy)
    net = sum(r[2] for r in rows)
    vat = sum(r[3] for r in rows)
    off = discounts.coupon_value(coupon, net)
    goods = net + vat - off
    weight = sum(r[0].weight_g * r[1] for r in rows)
    ship = shipping.cost(weight, goods)
    return {"net": net, "tax": vat, "coupon": off, "shipping": ship, "total": goods + ship}


def total(cart, catalog, **kw):
    return totals(cart, catalog, **kw)["total"]


def render(cart, catalog, **kw):
    out = []
    for item, qty, net, vat in lines(cart, catalog, kw.get("category_off"), kw.get("multibuy")):
        out.append(f"{item.sku:<8}{qty:>4}{money.fmt(net):>10}{money.fmt(vat):>8}")
    t = totals(cart, catalog, **kw)
    for k in ("net", "tax", "coupon", "shipping", "total"):
        out.append(f"{k:<12}{money.fmt(t[k]):>18}")
    return "\n".join(out)
''',
}

# Each bug: (name, file, correct text, buggy text). The buggy project applies all six.
BUGS = [
    ("parse-padding", "shop/money.py", 'frac = (frac + "00")[:2]', "frac = frac[:2]"),
    ("cart-overwrite", "shop/cart.py", "self._qty[sku] = self._qty.get(sku, 0) + qty", "self._qty[sku] = qty"),
    ("multibuy-formula", "shop/discounts.py", "paid = qty - (qty // (x + y)) * y", "paid = qty - (qty // x) * y"),
    ("coupon-basis", "shop/invoice.py", "off = discounts.coupon_value(coupon, net)", "off = discounts.coupon_value(coupon, sum(r[0].price * r[1] for r in rows))"),
    ("weight-boundary", "shop/shipping.py", "if weight_g <= 500:", "if weight_g < 500:"),
    ("tax-rounding", "shop/tax.py", "return money.percent(net, RATES.get(category, DEFAULT_RATE))", "return round(net * RATES.get(category, DEFAULT_RATE) / 100)"),
]

CATALOG = '''BK1, Field Guide, books, 12.5, 400
BK2, Atlas, books, 30, 1500
FD1, Coffee, food, 7.5, 250
TL1, Hammer, tools, 18.9, 600
TL2, Pliers, tools, 9.99, 250
'''

VISIBLE = {
"tests/__init__.py": "",
"tests/test_invoice.py": r'''import unittest

from shop import catalog, invoice
from shop.cart import Cart

CATALOG = catalog.load("""__CATALOG__""")


def cart(*pairs):
    c = Cart()
    for sku, qty in pairs:
        c.add(sku, qty)
    return c


class InvoiceTotals(unittest.TestCase):
    # Expected totals were worked out by hand from SPEC.md.

    def test_single_tool(self):
        # 9.99 net, 1.90 tax (19%), 250 g ships for 4.90
        self.assertEqual(invoice.total(cart(("TL2", 1)), CATALOG), 1679)

    def test_single_coffee(self):
        # 7.50 net, 0.53 tax (7% of 7.50 is 52.5 cents, rounded half up), 250 g ships for 4.90
        self.assertEqual(invoice.total(cart(("FD1", 1)), CATALOG), 1293)

    def test_coffee_pair(self):
        # 2 x 7.50 = 15.00 net, 1.05 tax, 500 g ships for 4.90
        self.assertEqual(invoice.total(cart(("FD1", 2)), CATALOG), 2095)

    def test_book_added_twice(self):
        # two Field Guides, added one at a time: 25.00 net, 1.75 tax, 800 g ships for 7.90
        self.assertEqual(invoice.total(cart(("BK1", 1), ("BK1", 1)), CATALOG), 3465)

    def test_books_with_category_discount(self):
        # 10% off books: Atlas 30.00 -> 27.00, tax 1.89; 1500 g ships for 7.90
        self.assertEqual(invoice.total(cart(("BK2", 1)), CATALOG, category_off={"books": 10}), 3679)

    def test_three_for_two_hammers(self):
        # 4 hammers on (2, 1): pay for 3 = 56.70 net, 10.77 tax, free shipping
        self.assertEqual(invoice.total(cart(("TL1", 4)), CATALOG, multibuy={"TL1": (2, 1)}), 6747)

    def test_coupon_needs_discounted_spend(self):
        # Atlas 30.00 at 10% off is 27.00, under the coupon's 28.00 minimum: no coupon
        t = invoice.totals(cart(("BK2", 1)), CATALOG, category_off={"books": 10}, coupon=(500, 2800))
        self.assertEqual(t["coupon"], 0)

    def test_mixed_basket(self):
        c = cart(("BK1", 1), ("FD1", 3), ("TL2", 2))
        t = invoice.totals(c, CATALOG, category_off={"books": 10}, multibuy={"FD1": (2, 1)}, coupon=(300, 2000))
        # BK1 11.25 + 0.79; FD1 pay 2 = 15.00 + 1.05; TL2 19.98 + 3.80
        self.assertEqual((t["net"], t["tax"], t["coupon"]), (4623, 564, 300))
        self.assertEqual(t["total"], 4887 + 790)


class Render(unittest.TestCase):
    def test_render_lists_lines_and_total(self):
        out = invoice.render(cart(("TL2", 1)), CATALOG)
        self.assertIn("TL2", out)
        self.assertTrue(out.splitlines()[-1].endswith("16.79"))


if __name__ == "__main__":
    unittest.main()
''',
}

HIDDEN = r'''import unittest

from shop import catalog, discounts, invoice, money, shipping, tax
from shop.cart import Cart


class Money(unittest.TestCase):
    def test_parse(self):
        for text, cents in [("12", 1200), ("12.5", 1250), ("12.50", 1250), ("0.05", 5), ("0.5", 50), ("3.07", 307)]:
            self.assertEqual(money.parse(text), cents, text)

    def test_percent_half_up(self):
        for cents, rate, want in [(750, 7, 53), (250, 7, 18), (1050, 7, 74), (150, 19, 29), (2500, 10, 250)]:
            self.assertEqual(money.percent(cents, rate), want, (cents, rate))


class CartAdd(unittest.TestCase):
    def test_add_accumulates(self):
        c = Cart()
        c.add("A", 2); c.add("B", 1); c.add("A", 3)
        self.assertEqual(c.lines(), [("A", 5), ("B", 1)])


class Multibuy(unittest.TestCase):
    def test_paid_units(self):
        item = catalog.Item("X", "x", "tools", 100, 1)
        for qty, offer, paid in [(7, (2, 1), 5), (3, (2, 1), 2), (2, (2, 1), 2), (8, (3, 1), 6), (5, (1, 1), 3)]:
            self.assertEqual(discounts.line_net(item, qty, multibuy={"X": offer}), paid * 100, (qty, offer))


class Coupon(unittest.TestCase):
    def test_threshold_on_discounted_net(self):
        cat = catalog.load("B, b, books, 30, 100")
        c = Cart(); c.add("B", 1)
        self.assertEqual(invoice.totals(c, cat, category_off={"books": 10}, coupon=(500, 2800))["coupon"], 0)
        self.assertEqual(invoice.totals(c, cat, category_off={"books": 10}, coupon=(500, 2700))["coupon"], 500)

    def test_never_below_zero(self):
        self.assertEqual(discounts.coupon_value((900, 0), 500), 500)


class Tax(unittest.TestCase):
    def test_line_half_up(self):
        for cat, net, want in [("food", 750, 53), ("books", 250, 18), ("tools", 150, 29), ("food", 2250, 158)]:
            self.assertEqual(tax.line_tax(cat, net), want, (cat, net))


class Shipping(unittest.TestCase):
    def test_tiers_inclusive(self):
        for w, want in [(1, 490), (500, 490), (501, 790), (2000, 790), (2001, 1290)]:
            self.assertEqual(shipping.cost(w, 0), want, w)

    def test_free_from_50(self):
        self.assertEqual(shipping.cost(3000, 5000), 0)
        self.assertEqual(shipping.cost(3000, 4999), 1290)


if __name__ == "__main__":
    unittest.main()
'''


def write(root, files):
    for rel, text in files.items():
        p = os.path.join(root, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w") as f:
            f.write(text)


def project(root, bugs):
    files = dict(FILES)
    for _, rel, good, bad in bugs:
        assert files[rel].count(good) == 1, (rel, good)
        files[rel] = files[rel].replace(good, bad)
    write(root, files)
    write(root, {k: v.replace("__CATALOG__", CATALOG) for k, v in VISIBLE.items()})
    write(root, {"SPEC.md": SPEC})


def run_suite(root, suite_dir=None):
    """Failing test ids of the visible suite, or of the hidden suite in suite_dir."""
    if suite_dir:
        cmd = [sys.executable, "-m", "unittest", "-v", "hidden_test"]
        env = dict(os.environ, PYTHONPATH=root + os.pathsep + suite_dir)
        cwd = suite_dir
    else:
        cmd = [sys.executable, "-m", "unittest", "-v"]
        env, cwd = dict(os.environ), root
    r = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=60)
    fails = {l.split(" ... ")[0] for l in r.stderr.splitlines() if l.endswith(("FAIL", "ERROR"))}
    ran = sum(1 for l in r.stderr.splitlines() if " ... " in l)
    return fails, ran


def check():
    ok = True
    with tempfile.TemporaryDirectory() as t:
        hid = os.path.join(t, "hidden"); os.makedirs(hid)
        write(hid, {"hidden_test.py": HIDDEN})
        ref = os.path.join(t, "ref"); project(ref, [])
        f, n = run_suite(ref); print(f"reference visible: {n - len(f)}/{n}"); ok &= not f
        f, n = run_suite(ref, hid); print(f"reference hidden: {n - len(f)}/{n}"); ok &= not f
        bug = os.path.join(t, "bug"); project(bug, BUGS)
        f, n = run_suite(bug); print(f"buggy visible: {n - len(f)}/{n}"); ok &= bool(f)
        f, n = run_suite(bug, hid); print(f"buggy hidden: {n - len(f)}/{n}")
        for b in BUGS:
            one = os.path.join(t, "one-" + b[0]); project(one, [b])
            fv, _ = run_suite(one); fh, _ = run_suite(one, hid)
            alone_ok = bool(fv) and bool(fh)
            print(f"  {b[0]:<17} alone fails visible {len(fv)}, hidden {len(fh)}")
            ok &= alone_ok
    print("CHECK", "OK" if ok else "FAILED")
    return ok


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "build":
        project(sys.argv[2], BUGS)
    elif cmd == "reference":
        project(sys.argv[2], [])
    elif cmd == "hidden":
        os.makedirs(sys.argv[2], exist_ok=True); write(sys.argv[2], {"hidden_test.py": HIDDEN})
    elif cmd == "check":
        sys.exit(0 if check() else 1)
