# Protocol Documentation
<a name="top"></a>

## Table of Contents

- [ecommerce/common/v1/money.proto](#ecommerce_common_v1_money-proto)
    - [Money](#ecommerce-common-v1-Money)
  
- [ecommerce/catalog/v1/product.proto](#ecommerce_catalog_v1_product-proto)
    - [Product](#ecommerce-catalog-v1-Product)
    - [Variant](#ecommerce-catalog-v1-Variant)
    - [Variant.AttributesEntry](#ecommerce-catalog-v1-Variant-AttributesEntry)
  
    - [ProductStatus](#ecommerce-catalog-v1-ProductStatus)
  
- [ecommerce/catalog/v1/catalog_service.proto](#ecommerce_catalog_v1_catalog_service-proto)
    - [AddVariantRequest](#ecommerce-catalog-v1-AddVariantRequest)
    - [AddVariantResponse](#ecommerce-catalog-v1-AddVariantResponse)
    - [ArchiveProductRequest](#ecommerce-catalog-v1-ArchiveProductRequest)
    - [ArchiveProductResponse](#ecommerce-catalog-v1-ArchiveProductResponse)
    - [CreateProductRequest](#ecommerce-catalog-v1-CreateProductRequest)
    - [CreateProductResponse](#ecommerce-catalog-v1-CreateProductResponse)
    - [GetProductRequest](#ecommerce-catalog-v1-GetProductRequest)
    - [GetProductResponse](#ecommerce-catalog-v1-GetProductResponse)
    - [GetProductsByIDsRequest](#ecommerce-catalog-v1-GetProductsByIDsRequest)
    - [GetProductsByIDsResponse](#ecommerce-catalog-v1-GetProductsByIDsResponse)
    - [GetVariantsBySKUsRequest](#ecommerce-catalog-v1-GetVariantsBySKUsRequest)
    - [GetVariantsBySKUsResponse](#ecommerce-catalog-v1-GetVariantsBySKUsResponse)
    - [ListProductsRequest](#ecommerce-catalog-v1-ListProductsRequest)
    - [ListProductsResponse](#ecommerce-catalog-v1-ListProductsResponse)
    - [NewVariant](#ecommerce-catalog-v1-NewVariant)
    - [NewVariant.AttributesEntry](#ecommerce-catalog-v1-NewVariant-AttributesEntry)
    - [PublishProductRequest](#ecommerce-catalog-v1-PublishProductRequest)
    - [PublishProductResponse](#ecommerce-catalog-v1-PublishProductResponse)
    - [UpdateProductRequest](#ecommerce-catalog-v1-UpdateProductRequest)
    - [UpdateProductResponse](#ecommerce-catalog-v1-UpdateProductResponse)
    - [UpdateVariantRequest](#ecommerce-catalog-v1-UpdateVariantRequest)
    - [UpdateVariantRequest.AttributesEntry](#ecommerce-catalog-v1-UpdateVariantRequest-AttributesEntry)
    - [UpdateVariantResponse](#ecommerce-catalog-v1-UpdateVariantResponse)
  
    - [CatalogService](#ecommerce-catalog-v1-CatalogService)
  
- [ecommerce/events/v1/envelope.proto](#ecommerce_events_v1_envelope-proto)
    - [EventEnvelope](#ecommerce-events-v1-EventEnvelope)
  
- [ecommerce/events/v1/identity.proto](#ecommerce_events_v1_identity-proto)
    - [UserRegistered](#ecommerce-events-v1-UserRegistered)
  
- [ecommerce/events/v1/order.proto](#ecommerce_events_v1_order-proto)
    - [OrderCancelled](#ecommerce-events-v1-OrderCancelled)
    - [OrderLine](#ecommerce-events-v1-OrderLine)
    - [OrderPaid](#ecommerce-events-v1-OrderPaid)
    - [OrderPlaced](#ecommerce-events-v1-OrderPlaced)
  
- [ecommerce/events/v1/payment.proto](#ecommerce_events_v1_payment-proto)
    - [PaymentFailed](#ecommerce-events-v1-PaymentFailed)
    - [PaymentSucceeded](#ecommerce-events-v1-PaymentSucceeded)
  
- [ecommerce/identity/v1/user.proto](#ecommerce_identity_v1_user-proto)
    - [User](#ecommerce-identity-v1-User)
  
- [ecommerce/identity/v1/identity_service.proto](#ecommerce_identity_v1_identity_service-proto)
    - [GetUserRequest](#ecommerce-identity-v1-GetUserRequest)
    - [GetUserResponse](#ecommerce-identity-v1-GetUserResponse)
    - [GetUsersByIDsRequest](#ecommerce-identity-v1-GetUsersByIDsRequest)
    - [GetUsersByIDsResponse](#ecommerce-identity-v1-GetUsersByIDsResponse)
    - [LoginRequest](#ecommerce-identity-v1-LoginRequest)
    - [LoginResponse](#ecommerce-identity-v1-LoginResponse)
    - [LogoutRequest](#ecommerce-identity-v1-LogoutRequest)
    - [LogoutResponse](#ecommerce-identity-v1-LogoutResponse)
    - [RefreshTokenRequest](#ecommerce-identity-v1-RefreshTokenRequest)
    - [RefreshTokenResponse](#ecommerce-identity-v1-RefreshTokenResponse)
    - [RegisterRequest](#ecommerce-identity-v1-RegisterRequest)
    - [RegisterResponse](#ecommerce-identity-v1-RegisterResponse)
    - [TokenPair](#ecommerce-identity-v1-TokenPair)
  
    - [IdentityService](#ecommerce-identity-v1-IdentityService)
  
- [ecommerce/inventory/v1/stock.proto](#ecommerce_inventory_v1_stock-proto)
    - [Reservation](#ecommerce-inventory-v1-Reservation)
    - [ReservationLine](#ecommerce-inventory-v1-ReservationLine)
    - [StockItem](#ecommerce-inventory-v1-StockItem)
  
    - [ReservationStatus](#ecommerce-inventory-v1-ReservationStatus)
  
- [ecommerce/inventory/v1/inventory_service.proto](#ecommerce_inventory_v1_inventory_service-proto)
    - [AdjustStockRequest](#ecommerce-inventory-v1-AdjustStockRequest)
    - [AdjustStockResponse](#ecommerce-inventory-v1-AdjustStockResponse)
    - [CommitReservationRequest](#ecommerce-inventory-v1-CommitReservationRequest)
    - [CommitReservationResponse](#ecommerce-inventory-v1-CommitReservationResponse)
    - [CreateStockItemRequest](#ecommerce-inventory-v1-CreateStockItemRequest)
    - [CreateStockItemResponse](#ecommerce-inventory-v1-CreateStockItemResponse)
    - [GetReservationRequest](#ecommerce-inventory-v1-GetReservationRequest)
    - [GetReservationResponse](#ecommerce-inventory-v1-GetReservationResponse)
    - [GetStockBySKUsRequest](#ecommerce-inventory-v1-GetStockBySKUsRequest)
    - [GetStockBySKUsResponse](#ecommerce-inventory-v1-GetStockBySKUsResponse)
    - [NewReservationLine](#ecommerce-inventory-v1-NewReservationLine)
    - [ReleaseReservationRequest](#ecommerce-inventory-v1-ReleaseReservationRequest)
    - [ReleaseReservationResponse](#ecommerce-inventory-v1-ReleaseReservationResponse)
    - [ReserveStockRequest](#ecommerce-inventory-v1-ReserveStockRequest)
    - [ReserveStockResponse](#ecommerce-inventory-v1-ReserveStockResponse)
  
    - [InventoryService](#ecommerce-inventory-v1-InventoryService)
  
- [ecommerce/order/v1/order.proto](#ecommerce_order_v1_order-proto)
    - [Order](#ecommerce-order-v1-Order)
    - [OrderLine](#ecommerce-order-v1-OrderLine)
  
    - [OrderStatus](#ecommerce-order-v1-OrderStatus)
  
- [ecommerce/order/v1/order_service.proto](#ecommerce_order_v1_order_service-proto)
    - [CheckoutLine](#ecommerce-order-v1-CheckoutLine)
    - [CheckoutRequest](#ecommerce-order-v1-CheckoutRequest)
    - [CheckoutResponse](#ecommerce-order-v1-CheckoutResponse)
    - [GetOrderRequest](#ecommerce-order-v1-GetOrderRequest)
    - [GetOrderResponse](#ecommerce-order-v1-GetOrderResponse)
    - [ListOrdersRequest](#ecommerce-order-v1-ListOrdersRequest)
    - [ListOrdersResponse](#ecommerce-order-v1-ListOrdersResponse)
  
    - [OrderService](#ecommerce-order-v1-OrderService)
  
- [ecommerce/payment/v1/payment.proto](#ecommerce_payment_v1_payment-proto)
    - [Payment](#ecommerce-payment-v1-Payment)
  
    - [PaymentStatus](#ecommerce-payment-v1-PaymentStatus)
  
- [ecommerce/payment/v1/payment_service.proto](#ecommerce_payment_v1_payment_service-proto)
    - [GetPaymentRequest](#ecommerce-payment-v1-GetPaymentRequest)
    - [GetPaymentResponse](#ecommerce-payment-v1-GetPaymentResponse)
    - [GetPaymentsByOrderIDsRequest](#ecommerce-payment-v1-GetPaymentsByOrderIDsRequest)
    - [GetPaymentsByOrderIDsResponse](#ecommerce-payment-v1-GetPaymentsByOrderIDsResponse)
    - [HandleProviderCallbackRequest](#ecommerce-payment-v1-HandleProviderCallbackRequest)
    - [HandleProviderCallbackResponse](#ecommerce-payment-v1-HandleProviderCallbackResponse)
    - [InitiatePaymentRequest](#ecommerce-payment-v1-InitiatePaymentRequest)
    - [InitiatePaymentResponse](#ecommerce-payment-v1-InitiatePaymentResponse)
  
    - [PaymentService](#ecommerce-payment-v1-PaymentService)
  
- [Scalar Value Types](#scalar-value-types)



<a name="ecommerce_common_v1_money-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/common/v1/money.proto



<a name="ecommerce-common-v1-Money"></a>

### Money
Money is an amount in one currency.

It lives here because a price quoted by catalog becomes a cart line and then
an order line without changing meaning, and three services inventing three
shapes for it is how a total ends up disagreeing with the sum of its parts.
This is the wire shape only — each service still owns a Money in its own
domain package, since the rules that protect an amount are the service&#39;s and
a domain that imported this file would be importing generated code.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| amount_minor | [int64](#int64) |  | The amount in the currency&#39;s minor unit: 1050 is THB 10.50, and 1050 in JPY is ¥1050 because yen has no minor unit — the number of decimal places is a property of currency_code, not of this field.

An integer rather than a decimal string because every arithmetic this system performs on a price is exact in minor units, and because a string would put a parse step between every service and the number. Signed because a discount and a refund are amounts too.

No bound is declared here. &#34;A price is not negative&#34; is a rule about prices, which catalog&#39;s domain holds; Money is also the shape of an adjustment, and a constraint on the message would forbid those everywhere it is reused. |
| currency_code | [string](#string) |  | ISO 4217, uppercase — &#34;THB&#34;, &#34;USD&#34;. Checked structurally here; whether this system actually sells in that currency is a question no shared message can answer. |





 

 

 

 



<a name="ecommerce_catalog_v1_product-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/catalog/v1/product.proto



<a name="ecommerce-catalog-v1-Product"></a>

### Product
Product is one thing on sale as the storefront describes it: the name, the
words, the category. It is never the thing that gets bought — that is a
Variant, and every service downstream of this one references those.

The outward shape, not the aggregate: the optimistic-locking version stays
inside the service, as does anything a future admin console needs and a
shopper does not.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| name | [string](#string) |  |  |
| description | [string](#string) |  |  |
| category | [string](#string) |  | A slug — &#34;clothing&#34;, &#34;clothing/shirts&#34;. Deliberately a string and not a Category message: nothing here needs a tree, localised category names, or a category that can be renamed in one place, and a taxonomy aggregate brings its own CRUD surface with it. It becomes an aggregate the day one of those is actually wanted, which is a field replaced rather than a service reshaped. |
| status | [ProductStatus](#ecommerce-catalog-v1-ProductStatus) |  |  |
| variants | [Variant](#ecommerce-catalog-v1-Variant) | repeated | Every variant of this product, including ones a shopper cannot buy today. Filtering is the caller&#39;s, because &#34;buyable&#34; means something different on a product page and in an admin list. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="ecommerce-catalog-v1-Variant"></a>

### Variant
Variant is the unit that is actually sold: one SKU, one price.

The split from Product exists even where a product will only ever have one
variant, because cart, inventory, and order all reference the sellable unit.
Adding this level of indirection later would be a breaking change in three
contracts and a migration in three databases; carrying it now costs one
table.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| product_id | [string](#string) |  |  |
| sku | [string](#string) |  | Unique across the service, uppercase. This is what the warehouse and the rest of the system call this thing, so it is chosen by whoever creates the product rather than generated here. |
| price | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  |  |
| attributes | [Variant.AttributesEntry](#ecommerce-catalog-v1-Variant-AttributesEntry) | repeated | What distinguishes this variant from its siblings — {&#34;size&#34;: &#34;M&#34;}, {&#34;weight_g&#34;: &#34;500&#34;}. Opaque on purpose: no rule in this service reads a key, which is exactly why the catalog can sell shirts and rice without knowing which it is doing. The day a rule does read one, that key is a field and not an entry here. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="ecommerce-catalog-v1-Variant-AttributesEntry"></a>

### Variant.AttributesEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [string](#string) |  |  |





 


<a name="ecommerce-catalog-v1-ProductStatus"></a>

### ProductStatus
ProductStatus is where a product sits in its lifecycle. The transitions
between these are the catalog&#39;s state machine and belong to its domain;
this enum only names the states so a caller can read them.

| Name | Number | Description |
| ---- | ------ | ----------- |
| PRODUCT_STATUS_UNSPECIFIED | 0 |  |
| PRODUCT_STATUS_DRAFT | 1 | Being written. Never shown to a shopper and never buyable. |
| PRODUCT_STATUS_ACTIVE | 2 | Published and buyable. |
| PRODUCT_STATUS_ARCHIVED | 3 | Withdrawn from sale, and kept forever rather than deleted: orders placed last year still name these variants, and a row a completed order points at must not disappear. |


 

 

 



<a name="ecommerce_catalog_v1_catalog_service-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/catalog/v1/catalog_service.proto



<a name="ecommerce-catalog-v1-AddVariantRequest"></a>

### AddVariantRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product_id | [string](#string) |  |  |
| variant | [NewVariant](#ecommerce-catalog-v1-NewVariant) |  |  |






<a name="ecommerce-catalog-v1-AddVariantResponse"></a>

### AddVariantResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  | The whole product, not the one variant. Adding a variant is a change to the product, and a caller that has to merge a returned fragment into a copy it already holds will eventually merge it wrong. |






<a name="ecommerce-catalog-v1-ArchiveProductRequest"></a>

### ArchiveProductRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-catalog-v1-ArchiveProductResponse"></a>

### ArchiveProductResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |






<a name="ecommerce-catalog-v1-CreateProductRequest"></a>

### CreateProductRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| description | [string](#string) |  |  |
| category | [string](#string) |  |  |
| variants | [NewVariant](#ecommerce-catalog-v1-NewVariant) | repeated | May be empty: a draft is allowed to exist before anyone has decided what it costs. Publishing is where that stops being allowed. |






<a name="ecommerce-catalog-v1-CreateProductResponse"></a>

### CreateProductResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |






<a name="ecommerce-catalog-v1-GetProductRequest"></a>

### GetProductRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| status | [ProductStatus](#ecommerce-catalog-v1-ProductStatus) |  | Unset means ACTIVE only, exactly as it does on a listing.

A read by id has to be told, where a listing could have been left to its own default: there is no page to scope it and no filter a caller forgot, only an id somebody already has. Answering with whatever state that id is in would put an unfinished draft on a storefront for anyone who guessed a UUID, and the first sign of it would be the draft on a screen. |






<a name="ecommerce-catalog-v1-GetProductResponse"></a>

### GetProductResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |






<a name="ecommerce-catalog-v1-GetProductsByIDsRequest"></a>

### GetProductsByIDsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| ids | [string](#string) | repeated | Bounded because the response is: an unbounded id list is how a batch read drives its own callee out of memory. |






<a name="ecommerce-catalog-v1-GetProductsByIDsResponse"></a>

### GetProductsByIDsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| products | [Product](#ecommerce-catalog-v1-Product) | repeated | Products that exist, in no guaranteed order and possibly fewer than were asked for. A missing id is not an error: one archived product must not fail the whole screen a BFF is assembling. |






<a name="ecommerce-catalog-v1-GetVariantsBySKUsRequest"></a>

### GetVariantsBySKUsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| skus | [string](#string) | repeated | Bounded because the response is: an unbounded list is how a batch read drives its own callee out of memory. |






<a name="ecommerce-catalog-v1-GetVariantsBySKUsResponse"></a>

### GetVariantsBySKUsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| variants | [Variant](#ecommerce-catalog-v1-Variant) | repeated | In no guaranteed order, and possibly fewer than were asked for. A SKU that is not here is one nobody can buy right now. |






<a name="ecommerce-catalog-v1-ListProductsRequest"></a>

### ListProductsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| category | [string](#string) |  | Exact match on the slug, or every category when empty. |
| status | [ProductStatus](#ecommerce-catalog-v1-ProductStatus) |  | Unset means ACTIVE only.

The default is a filter rather than &#34;everything&#34; because the storefront is the caller, and of the two ways to be wrong here, showing a shopper an unfinished draft is the one nobody notices until it is on a screen. |
| page_size | [int32](#int32) |  | 0 means 20. |
| page_token | [string](#string) |  | next_page_token from the previous response, opaque to the caller. A cursor and not an offset: rows are inserted while a shopper pages, and an offset would skip or repeat whatever moved across the boundary. |






<a name="ecommerce-catalog-v1-ListProductsResponse"></a>

### ListProductsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| products | [Product](#ecommerce-catalog-v1-Product) | repeated |  |
| next_page_token | [string](#string) |  | Empty on the last page. |






<a name="ecommerce-catalog-v1-NewVariant"></a>

### NewVariant
NewVariant is a variant that does not exist yet — the fields a caller
supplies, without the id and timestamps the service assigns. A separate
message from Variant so that neither has fields the other must ignore.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  | Uppercase, so uniqueness never depends on how it was typed. Chosen by the caller rather than generated here because a SKU is what the warehouse already calls this thing. |
| price | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  |  |
| attributes | [NewVariant.AttributesEntry](#ecommerce-catalog-v1-NewVariant-AttributesEntry) | repeated |  |






<a name="ecommerce-catalog-v1-NewVariant-AttributesEntry"></a>

### NewVariant.AttributesEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [string](#string) |  |  |






<a name="ecommerce-catalog-v1-PublishProductRequest"></a>

### PublishProductRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-catalog-v1-PublishProductResponse"></a>

### PublishProductResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |






<a name="ecommerce-catalog-v1-UpdateProductRequest"></a>

### UpdateProductRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| name | [string](#string) |  |  |
| description | [string](#string) |  |  |
| category | [string](#string) |  |  |






<a name="ecommerce-catalog-v1-UpdateProductResponse"></a>

### UpdateProductResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |






<a name="ecommerce-catalog-v1-UpdateVariantRequest"></a>

### UpdateVariantRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product_id | [string](#string) |  | The aggregate root, named even though variant_id alone would find it: a variant is only ever modified as part of its product, and passing both means a caller cannot ask this service to reprice something under a product it was not looking at. |
| variant_id | [string](#string) |  |  |
| price | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  |  |
| attributes | [UpdateVariantRequest.AttributesEntry](#ecommerce-catalog-v1-UpdateVariantRequest-AttributesEntry) | repeated |  |






<a name="ecommerce-catalog-v1-UpdateVariantRequest-AttributesEntry"></a>

### UpdateVariantRequest.AttributesEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [string](#string) |  |  |






<a name="ecommerce-catalog-v1-UpdateVariantResponse"></a>

### UpdateVariantResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| product | [Product](#ecommerce-catalog-v1-Product) |  |  |





 

 

 


<a name="ecommerce-catalog-v1-CatalogService"></a>

### CatalogService
CatalogService owns products, their variants, and the prices on them. It is
reached over east-west gRPC only.

It owns no stock. What is on the shelf is written far more often than it is
described, and the rule that protects it — never sell the same unit twice —
cannot be enforced without reservations and expiries, which are a different
aggregate with a different vocabulary. That service arrives with the order
flow; this one answers what a thing is and what it costs.

Every field constraint below is declared here rather than checked in a
handler: a single interceptor enforces them, so a handler that validates its
own input is a bug wherever it appears. What is *not* here is anything that
needs the aggregate to answer it — that a published product has at least one
variant, that an archived one takes no edits, that every variant of one
product is priced in one currency. Those are invariants, they live in the
domain, and a constraint here could only ever repeat the easy half of them.

| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| GetProduct | [GetProductRequest](#ecommerce-catalog-v1-GetProductRequest) | [GetProductResponse](#ecommerce-catalog-v1-GetProductResponse) | GetProduct reads one product with its variants, in the state the caller asked for and not found in any other. A storefront that names no state is answered about published products alone. |
| GetProductsByIDs | [GetProductsByIDsRequest](#ecommerce-catalog-v1-GetProductsByIDsRequest) | [GetProductsByIDsResponse](#ecommerce-catalog-v1-GetProductsByIDsResponse) | GetProductsByIDs reads many. Every service exposes one of these so a BFF can fill a screen without looping single-item calls. |
| ListProducts | [ListProductsRequest](#ecommerce-catalog-v1-ListProductsRequest) | [ListProductsResponse](#ecommerce-catalog-v1-ListProductsResponse) | ListProducts pages through the catalog, newest first. |
| GetVariantsBySKUs | [GetVariantsBySKUsRequest](#ecommerce-catalog-v1-GetVariantsBySKUsRequest) | [GetVariantsBySKUsResponse](#ecommerce-catalog-v1-GetVariantsBySKUsResponse) | GetVariantsBySKUs reads the sellable units named by their SKUs, which is what a caller holding a cart has: an order names SKUs and never product ids, because a shopper picked a size rather than a product.

Only variants of an active product come back. A SKU belonging to a draft or an archived product is absent, exactly as an unknown one is — to whoever is trying to buy it the two are the same, and the difference is a fact about the shop&#39;s own back office. |
| CreateProduct | [CreateProductRequest](#ecommerce-catalog-v1-CreateProductRequest) | [CreateProductResponse](#ecommerce-catalog-v1-CreateProductResponse) | CreateProduct creates a product, in draft, optionally with variants.

Deliberately not named Get* or Batch*: the client retry policy reads method names to decide what is safe to retry, and a retried create is a second product. |
| UpdateProduct | [UpdateProductRequest](#ecommerce-catalog-v1-UpdateProductRequest) | [UpdateProductResponse](#ecommerce-catalog-v1-UpdateProductResponse) | UpdateProduct replaces the descriptive fields of a product.

Wholesale rather than a patch: with three fields, a field mask buys nothing but a second way to be wrong about which of them the caller meant to clear. A caller sends what the product should now say. |
| AddVariant | [AddVariantRequest](#ecommerce-catalog-v1-AddVariantRequest) | [AddVariantResponse](#ecommerce-catalog-v1-AddVariantResponse) | AddVariant adds a sellable unit to a product. |
| UpdateVariant | [UpdateVariantRequest](#ecommerce-catalog-v1-UpdateVariantRequest) | [UpdateVariantResponse](#ecommerce-catalog-v1-UpdateVariantResponse) | UpdateVariant reprices a variant or restates what distinguishes it.

The SKU is not among the fields it can change. Orders, carts, and the warehouse all refer to a variant by that string, so renaming one would rename a thing other systems have already written down. |
| PublishProduct | [PublishProductRequest](#ecommerce-catalog-v1-PublishProductRequest) | [PublishProductResponse](#ecommerce-catalog-v1-PublishProductResponse) | PublishProduct moves a draft into the storefront. |
| ArchiveProduct | [ArchiveProductRequest](#ecommerce-catalog-v1-ArchiveProductRequest) | [ArchiveProductResponse](#ecommerce-catalog-v1-ArchiveProductResponse) | ArchiveProduct withdraws a product from sale. Nothing is deleted, and the move is one-way — a product that should sell again is a new one, because reviving an archived product silently revives whatever was wrong with it. |

 



<a name="ecommerce_events_v1_envelope-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/events/v1/envelope.proto



<a name="ecommerce-events-v1-EventEnvelope"></a>

### EventEnvelope
EventEnvelope is what every message on every topic actually is: the routing,
ordering, and tracing a consumer needs, wrapped around a payload it decides
for itself whether to open.

One envelope for the whole system rather than one per topic, because
everything that reads a message before dispatching it — the relay, a DLQ
router, a consumer&#39;s idempotency claim — needs these fields and needs them in
the same place regardless of which topic it is reading.

Nothing here is declared with protovalidate. A Kafka message reaches no
interceptor, so a constraint on this message would be enforced by nobody
while reading, in the contract, exactly like a guarantee.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| event_id | [string](#string) |  | Unique per event, a UUID minted when the event was raised. It is what a consumer claims in processed_events, so a redelivery carries the same value and a genuinely new event never does. Not the outbox row id: that is unique only within one service&#39;s database. |
| event_type | [string](#string) |  | The name consumers dispatch on, e.g. &#34;OrderPaid&#34; — the vocabulary of the service that owns the aggregate. It is API: a consumer branches on it, so renaming one is a breaking change even though no generated code mentions it. Kept beside the payload&#39;s own type_url rather than derived from it, because the type is where a message is defined and this is what it means. |
| aggregate_id | [string](#string) |  | The aggregate the event happened to, and the Kafka message key, which is what keeps two events for one order in the order they were raised. |
| version | [int64](#int64) |  | The aggregate&#39;s version at the moment the event was raised — the same optimistic-locking counter the row carries. A consumer that keeps its own copy of an aggregate uses it to ignore what it has already seen; one that does not can ignore this field. |
| occurred_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | When the fact became true, decided by the producer rather than by whoever reads it. An event may be published seconds after it was raised and consumed hours after that, so this is the only timestamp with a fixed meaning. |
| correlation_id | [string](#string) |  | The one user-visible operation this event descends from, carried across the hop where the context that produced it is long gone. Without it a checkout and the emails it caused are unrelated lines in the log. |
| traceparent | [string](#string) |  | W3C trace context, so a consumer&#39;s spans attach to the trace that caused them instead of starting a second one nobody can find. |
| payload | [google.protobuf.Any](#google-protobuf-Any) |  | The event itself. Any rather than bytes so the payload names its own type: a consumer handed something it did not expect refuses it, instead of unmarshalling one message&#39;s bytes into another message&#39;s fields and succeeding. |





 

 

 

 



<a name="ecommerce_events_v1_identity-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/events/v1/identity.proto



<a name="ecommerce-events-v1-UserRegistered"></a>

### UserRegistered
UserRegistered says that an account now exists. Published to
ecommerce.identity.events.v1.

The fields are declared here rather than by embedding
ecommerce.identity.v1.User, though the two overlap today. That message is the
answer to GetUser and may be changed to suit callers of it; this one is a
fact already written to a topic, which consumers older than the producer are
still reading. Sharing the shape would make one contract&#39;s change the other
contract&#39;s break.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| user_id | [string](#string) |  |  |
| email | [string](#string) |  | Carried because a consumer that wants to welcome this user would otherwise call identity back for it — for every event, at consumer speed. |
| roles | [string](#string) | repeated | What the account was created with, which is not what it holds now: a role granted later is its own event, and reading this one as current is how a consumer builds a stale copy of a user. |
| registered_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |





 

 

 

 



<a name="ecommerce_events_v1_order-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/events/v1/order.proto



<a name="ecommerce-events-v1-OrderCancelled"></a>

### OrderCancelled
OrderCancelled says an order will not be fulfilled and whatever it held is
to be given back. Published to ecommerce.order.events.v1.

The order service consumes this one back itself too, for the same durability
reason: releasing the reservation is the compensating step, and it belongs on
the topic rather than in whatever process happened to decide the order was
over.

It carries no reason. Why an order ended is a fact about the payment or the
timeout that ended it, published by whoever knew it; this event says only
that the stock is free again.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| order_id | [string](#string) |  |  |
| user_id | [string](#string) |  |  |
| reservation_id | [string](#string) |  | The hold to give back. Releasing one already released succeeds and changes nothing, which is what lets this event be redelivered. |
| cancelled_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="ecommerce-events-v1-OrderLine"></a>

### OrderLine
OrderLine is one SKU as it was bought — the price frozen at checkout, not
whatever the catalog says now.

Declared here rather than reusing ecommerce.order.v1.OrderLine, though the
two are the same shape today. That one answers GetOrder and may be changed to
suit its callers; this one is a fact already on a topic that consumers older
than the producer are still reading.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| quantity | [int32](#int32) |  |  |
| unit_price | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  |  |






<a name="ecommerce-events-v1-OrderPaid"></a>

### OrderPaid
OrderPaid says the money for an order is in. Published to
ecommerce.order.events.v1.

The order service consumes this one back itself: turning the hold on stock
into a sale is a gRPC call to inventory, and a call made straight after the
transaction commits is a write nothing would retry if the process died
between the two. Reading the fact back off the topic makes the trigger as
durable as the payment.

It is deliberately OrderPlaced that does not carry that job. Committing a
hold is selling the goods, and doing it before the money is in leaves a
failed payment with nothing to give back — inventory refuses to release a
reservation it has already committed.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| order_id | [string](#string) |  |  |
| user_id | [string](#string) |  |  |
| reservation_id | [string](#string) |  | The hold taken at checkout, carried so the consumer that commits it needs no read of the order first. |
| total | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  | What was actually paid, which is the order&#39;s total: this service takes no partial payments. |
| paid_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="ecommerce-events-v1-OrderPlaced"></a>

### OrderPlaced
OrderPlaced says that an order exists and is waiting for payment, with stock
already held for it. Published to ecommerce.order.events.v1.

Nothing has been sold yet and no money has moved: the customer is on their
way to a payment screen, and the reservation&#39;s expires_at is how long they
have to reach it. What the order becomes is said by OrderPaid or
OrderCancelled.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| order_id | [string](#string) |  |  |
| user_id | [string](#string) |  |  |
| reservation_id | [string](#string) |  | The hold taken before the order was persisted. Inventory refuses to commit one whose expires_at has passed, so this is also a deadline on how long the rest of the flow has. |
| lines | [OrderLine](#ecommerce-events-v1-OrderLine) | repeated |  |
| total | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  |  |
| placed_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |





 

 

 

 



<a name="ecommerce_events_v1_payment-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/events/v1/payment.proto



<a name="ecommerce-events-v1-PaymentFailed"></a>

### PaymentFailed
PaymentFailed says one attempt to collect is over and no money moved.
Published to ecommerce.payment.events.v1.

It does not say the order is dead. A customer whose card was declined may try
another one while their stock is still held, so the order service cancels on
this only when it has decided not to wait any longer — which is a decision
about an order, and therefore the order service&#39;s to make.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| payment_id | [string](#string) |  |  |
| order_id | [string](#string) |  |  |
| user_id | [string](#string) |  |  |
| amount | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  |  |
| reason | [string](#string) |  | Why, in the provider&#39;s vocabulary. A developer and support aid: the set of these belongs to the provider and changes without this system being told, so no consumer may branch on it. |
| failed_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="ecommerce-events-v1-PaymentSucceeded"></a>

### PaymentSucceeded
PaymentSucceeded says the money for an order is in. Published to
ecommerce.payment.events.v1.

The order service consumes it and marks its order paid, which is what
eventually turns the hold on stock into a sale. Two hops through Kafka rather
than one: this event is a fact about a payment, and OrderPaid is a fact about
an order, and collapsing them would put the order&#39;s state machine inside a
consumer that does not own it.

The key is the payment id and not the order id, so redelivered attempts
against one order stay ordered per attempt. A consumer that needs them
ordered per order has the order id in the body.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| payment_id | [string](#string) |  |  |
| order_id | [string](#string) |  |  |
| user_id | [string](#string) |  |  |
| amount | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  | What was actually collected. It is the order&#39;s total: this system takes no partial payments, and a consumer comparing the two is how a mismatch with the provider gets noticed at all. |
| provider_reference | [string](#string) |  | The provider&#39;s own identifier for the charge, carried so that an order can be traced to a line in the provider&#39;s dashboard without a second read. |
| paid_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |





 

 

 

 



<a name="ecommerce_identity_v1_user-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/identity/v1/user.proto



<a name="ecommerce-identity-v1-User"></a>

### User
User is what the identity service tells everyone else about a person.

It is the outward shape, not the aggregate: the password hash, the refresh
tokens, and the optimistic-locking version stay inside the service and never
appear here. A field on this message is a promise to every caller, so it is
far cheaper to add one later than to take one back.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  | Stable identifier, a UUID. It is also the &#34;sub&#34; claim of every access token issued for this user, so nothing else may be used to identify them. |
| email | [string](#string) |  | Login identity, unique across the service and stored lowercased. |
| roles | [string](#string) | repeated | What the user is, never what they may do with a particular aggregate — that rule belongs to whichever service owns the aggregate. These are the roles copied into the &#34;roles&#34; claim at sign time. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |





 

 

 

 



<a name="ecommerce_identity_v1_identity_service-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/identity/v1/identity_service.proto



<a name="ecommerce-identity-v1-GetUserRequest"></a>

### GetUserRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-identity-v1-GetUserResponse"></a>

### GetUserResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| user | [User](#ecommerce-identity-v1-User) |  |  |






<a name="ecommerce-identity-v1-GetUsersByIDsRequest"></a>

### GetUsersByIDsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| ids | [string](#string) | repeated | Bounded because the response is: an unbounded id list is how a batch read drives its own callee out of memory. |






<a name="ecommerce-identity-v1-GetUsersByIDsResponse"></a>

### GetUsersByIDsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| users | [User](#ecommerce-identity-v1-User) | repeated | Users present in the store, in no guaranteed order and possibly fewer than were asked for. A missing id is not an error: one deleted user must not fail the whole screen the Composition API is assembling. |






<a name="ecommerce-identity-v1-LoginRequest"></a>

### LoginRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| email | [string](#string) |  |  |
| password | [string](#string) |  | Bounded, but with no minimum length beyond one byte: the rule the caller has to satisfy is whatever their password already is, and a policy tightened after they registered must not lock them out of an account they can still open. The upper bound is here for the same reason as on RegisterRequest — the value is handed to a deliberately slow hash. |






<a name="ecommerce-identity-v1-LoginResponse"></a>

### LoginResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tokens | [TokenPair](#ecommerce-identity-v1-TokenPair) |  |  |
| user | [User](#ecommerce-identity-v1-User) |  | The user, so the screen that just signed them in does not immediately ask for what this call already loaded. Unlike RegisterResponse, which returns no tokens, there is nothing to separate here: the caller proved who they are one field ago. |






<a name="ecommerce-identity-v1-LogoutRequest"></a>

### LogoutRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| refresh_token | [string](#string) |  |  |






<a name="ecommerce-identity-v1-LogoutResponse"></a>

### LogoutResponse
LogoutResponse is empty, and is a message rather than google.protobuf.Empty
so that the day it carries something — how many sessions were revoked, say —
is a field added rather than a signature changed.






<a name="ecommerce-identity-v1-RefreshTokenRequest"></a>

### RefreshTokenRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| refresh_token | [string](#string) |  | The opaque string from a previous TokenPair. The bound is generous rather than exact so the encoding can change without a contract change; what matters is that an unbounded string never reaches a hash function. |






<a name="ecommerce-identity-v1-RefreshTokenResponse"></a>

### RefreshTokenResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tokens | [TokenPair](#ecommerce-identity-v1-TokenPair) |  | A new pair every time, including a new refresh token. The one presented is spent by the time this returns. |






<a name="ecommerce-identity-v1-RegisterRequest"></a>

### RegisterRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| email | [string](#string) |  | Bounded at 254 bytes, the longest address SMTP is required to accept. |
| password | [string](#string) |  | Length is all that is checked here. Composition rules (&#34;one digit, one symbol&#34;) push users toward predictable passwords and belong nowhere near a transport contract.

The upper bound exists so an unbounded input cannot be handed to a deliberately slow hash. If bcrypt is ever chosen over argon2id it must drop to 72: bcrypt silently truncates there, which would make two different long passwords authenticate each other. |






<a name="ecommerce-identity-v1-RegisterResponse"></a>

### RegisterResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| user | [User](#ecommerce-identity-v1-User) |  | The created user, and no tokens. Registering and signing in are separate acts — bundling them means a client that only wanted to create an account is handed a credential it has to decide what to do with, and it hides the case where registration succeeds but sign-in should not be allowed yet. |






<a name="ecommerce-identity-v1-TokenPair"></a>

### TokenPair
TokenPair is what a successful sign-in or refresh hands back.

One message shared by both responses, so a field added for one is present on
the other. A client that has to read two shapes to learn the same fact ends
up with two code paths that drift.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| access_token | [string](#string) |  | A signed ES256 JWT, carried as the Authorization bearer token on every subsequent request and verified by Kong and the Composition API without either of them asking this service. That is the point of the asymmetry: only identity holds the private key, and everything else holds a key that can say no. |
| refresh_token | [string](#string) |  | An opaque random string, deliberately not a JWT.

A refresh token has to be revocable, and a self-contained signed token cannot be — logout would have to wait out the TTL. This one means nothing on its own: the service stores a hash of it in a row, and deleting the row is what ends the session. |
| access_token_expires_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | When access_token stops being accepted. Absolute rather than a duration in seconds, because the token itself carries an absolute &#34;exp&#34; and a client comparing the two should not have to reconstruct one from the other. |
| refresh_token_expires_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | When refresh_token stops being accepted, after which the user signs in again. Rotation does not extend it: the chain has a fixed end, or a stolen token that is refreshed often enough would never expire. |





 

 

 


<a name="ecommerce-identity-v1-IdentityService"></a>

### IdentityService
IdentityService owns users, their credentials, and the tokens minted from
them. It is reached over east-west gRPC only — the edge is Kong and the
Composition API, and no service in this repo speaks HTTP.

Every field constraint below is declared here rather than checked in a
handler: a single interceptor enforces them, so a handler that validates its
own input is a bug wherever it appears.

| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| Register | [RegisterRequest](#ecommerce-identity-v1-RegisterRequest) | [RegisterResponse](#ecommerce-identity-v1-RegisterResponse) | Register creates a user from an email and a password.

Deliberately not named Create*, and deliberately not a read: the client retry policy treats Get/List/Batch/Search/Count/Check as idempotent and retries them on Unavailable, which would turn one slow registration into several accounts. |
| GetUser | [GetUserRequest](#ecommerce-identity-v1-GetUserRequest) | [GetUserResponse](#ecommerce-identity-v1-GetUserResponse) | GetUser reads one user by ID. |
| GetUsersByIDs | [GetUsersByIDsRequest](#ecommerce-identity-v1-GetUsersByIDsRequest) | [GetUsersByIDsResponse](#ecommerce-identity-v1-GetUsersByIDsResponse) | GetUsersByIDs reads many. Every service exposes one of these so the Composition API can fill a screen without looping single-item calls. |
| Login | [LoginRequest](#ecommerce-identity-v1-LoginRequest) | [LoginResponse](#ecommerce-identity-v1-LoginResponse) | Login exchanges credentials for a token pair.

Named for the act rather than for the read it resembles: the client retry policy treats Get/List/Batch/Search/Count/Check as idempotent, and every attempt here mints a refresh token that is stored, so a retried Login would leave rows nobody holds.

A wrong password and an address that was never registered are answered identically — Unauthenticated with INVALID_CREDENTIALS — because any difference between them turns this RPC into a way to ask which email addresses have accounts. |
| RefreshToken | [RefreshTokenRequest](#ecommerce-identity-v1-RefreshTokenRequest) | [RefreshTokenResponse](#ecommerce-identity-v1-RefreshTokenResponse) | RefreshToken exchanges a refresh token for a new pair and invalidates the one presented.

Rotation on every call is what makes a stolen refresh token detectable: the thief and the legitimate client cannot both keep using the chain, and whichever presents the spent token second reveals that a copy exists. The service then revokes the whole chain, so the answer to a theft is that both parties have to sign in again — which the real user can do and the thief cannot.

That detection is also why this RPC is not idempotent and must never be retried automatically: a retry presents a token the first attempt already spent, which is indistinguishable from the theft it is designed to catch. |
| Logout | [LogoutRequest](#ecommerce-identity-v1-LogoutRequest) | [LogoutResponse](#ecommerce-identity-v1-LogoutResponse) | Logout revokes the presented refresh token and the rest of its rotation chain, so no further pair can be minted from it.

It cannot revoke the access token already in the caller&#39;s hands — a self-contained signed token is valid until it expires, and that window is the whole reason the TTL is 15 minutes. A screen that must stop working immediately has to ask the owning service, not the token.

Revoking something already revoked, expired, or never issued succeeds. Logging out is a state the caller wants to reach rather than a change they are making, a client retrying after a timeout must not see a failure, and an error here would tell a prober which tokens exist. |

 



<a name="ecommerce_inventory_v1_stock-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/inventory/v1/stock.proto



<a name="ecommerce-inventory-v1-Reservation"></a>

### Reservation
Reservation is one order&#39;s claim on stock, held for a while and then either
committed or given back.

The whole order is one reservation rather than one per line, because it is
released and committed as a unit: a saga that compensated line by line could
leave half an order holding capacity nobody will ever buy.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| order_id | [string](#string) |  | The order this was taken for. Unique, which is what makes ReserveStock idempotent — a saga that retries after an ambiguous timeout gets the same reservation back rather than a second hold on the same goods. |
| lines | [ReservationLine](#ecommerce-inventory-v1-ReservationLine) | repeated |  |
| status | [ReservationStatus](#ecommerce-inventory-v1-ReservationStatus) |  |  |
| expires_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | When a held reservation stops being honoured. Past this the capacity is the reaper&#39;s to return, and CommitReservation refuses — see the RPC. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="ecommerce-inventory-v1-ReservationLine"></a>

### ReservationLine
ReservationLine is one SKU&#39;s share of a reservation.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| quantity | [int32](#int32) |  |  |






<a name="ecommerce-inventory-v1-StockItem"></a>

### StockItem
StockItem is how many of one sellable unit the warehouse has, split by what
is still sellable and what an order has already spoken for.

It is keyed by SKU rather than by variant id. The catalog mints the id, but
the SKU is what a shelf, a picking list, and a supplier&#39;s invoice all say, and
a service that counted physical things by a UUID would be unable to answer the
question a human actually asks.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| available | [int32](#int32) |  | What a new reservation may still take. |
| reserved | [int32](#int32) |  | What live reservations are holding. It becomes available again when a reservation is released or expires, and simply disappears when one is committed — the goods left the building. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |





 


<a name="ecommerce-inventory-v1-ReservationStatus"></a>

### ReservationStatus
ReservationStatus is where a reservation sits in its lifecycle. The
transitions between these are this service&#39;s state machine and belong to its
domain; this enum only names the states so a caller can read them.

| Name | Number | Description |
| ---- | ------ | ----------- |
| RESERVATION_STATUS_UNSPECIFIED | 0 |  |
| RESERVATION_STATUS_HELD | 1 | Holding capacity, and doing so until expires_at. |
| RESERVATION_STATUS_COMMITTED | 2 | The goods were sold. The held quantity is gone rather than returned. |
| RESERVATION_STATUS_RELEASED | 3 | Given back on purpose — the compensating step of a saga that failed further along. |
| RESERVATION_STATUS_EXPIRED | 4 | Given back by the reaper, because nobody committed it in time. Distinct from RELEASED so that a stranded saga is visible as such instead of looking like a compensation somebody ran. |


 

 

 



<a name="ecommerce_inventory_v1_inventory_service-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/inventory/v1/inventory_service.proto



<a name="ecommerce-inventory-v1-AdjustStockRequest"></a>

### AdjustStockRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| delta | [int32](#int32) |  | Non-zero, and either sign. An adjustment that would take available below zero is refused rather than clamped — the count is wrong either way, and clamping hides which. |
| reason | [string](#string) |  | Why the count moved, for the humans reading the log line. Free text and not an enum: no rule here reads it, and an enum would be a list to extend every time a warehouse invents a new way to lose a box. |






<a name="ecommerce-inventory-v1-AdjustStockResponse"></a>

### AdjustStockResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| item | [StockItem](#ecommerce-inventory-v1-StockItem) |  |  |






<a name="ecommerce-inventory-v1-CommitReservationRequest"></a>

### CommitReservationRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-inventory-v1-CommitReservationResponse"></a>

### CommitReservationResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| reservation | [Reservation](#ecommerce-inventory-v1-Reservation) |  |  |






<a name="ecommerce-inventory-v1-CreateStockItemRequest"></a>

### CreateStockItemRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| available | [int32](#int32) |  | May be zero: a SKU that is tracked but not yet delivered is an ordinary state, and it is the one that keeps an order from being taken for it. |






<a name="ecommerce-inventory-v1-CreateStockItemResponse"></a>

### CreateStockItemResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| item | [StockItem](#ecommerce-inventory-v1-StockItem) |  |  |






<a name="ecommerce-inventory-v1-GetReservationRequest"></a>

### GetReservationRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-inventory-v1-GetReservationResponse"></a>

### GetReservationResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| reservation | [Reservation](#ecommerce-inventory-v1-Reservation) |  |  |






<a name="ecommerce-inventory-v1-GetStockBySKUsRequest"></a>

### GetStockBySKUsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| skus | [string](#string) | repeated | Bounded because the response is: an unbounded list is how a batch read drives its own callee out of memory. |






<a name="ecommerce-inventory-v1-GetStockBySKUsResponse"></a>

### GetStockBySKUsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| items | [StockItem](#ecommerce-inventory-v1-StockItem) | repeated | The SKUs this service tracks, in no guaranteed order and possibly fewer than were asked for. A SKU nobody has stocked yet is not an error: one such line must not fail the whole screen a caller is assembling. |






<a name="ecommerce-inventory-v1-NewReservationLine"></a>

### NewReservationLine
NewReservationLine is one SKU a caller wants held.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| quantity | [int32](#int32) |  |  |






<a name="ecommerce-inventory-v1-ReleaseReservationRequest"></a>

### ReleaseReservationRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-inventory-v1-ReleaseReservationResponse"></a>

### ReleaseReservationResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| reservation | [Reservation](#ecommerce-inventory-v1-Reservation) |  |  |






<a name="ecommerce-inventory-v1-ReserveStockRequest"></a>

### ReserveStockRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| order_id | [string](#string) |  | The order taking the hold, and the idempotency key. A caller that has no order id yet has nothing to reserve against — the aggregate is persisted after this call returns, but its identifier is minted before. |
| lines | [NewReservationLine](#ecommerce-inventory-v1-NewReservationLine) | repeated | Two lines naming the same SKU are summed rather than refused: a cart that added the same shirt twice is describing one quantity, and the caller should not have to know that. |






<a name="ecommerce-inventory-v1-ReserveStockResponse"></a>

### ReserveStockResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| reservation | [Reservation](#ecommerce-inventory-v1-Reservation) |  |  |





 

 

 


<a name="ecommerce-inventory-v1-InventoryService"></a>

### InventoryService
InventoryService owns how many of each SKU exist and who has claimed them. It
is reached over east-west gRPC only.

It owns no prices and no descriptions. The catalog answers what a thing is and
what it costs; this service answers whether there is one left, which is written
far more often than it is read and is protected by a rule the catalog has no
way to enforce: never sell the same unit twice.

The one rule worth stating at this level is that reserving is synchronous. It
is the step an order takes before it persists anything, so that a shopper
learns about a sold-out SKU while they are still looking at the screen rather
than in an email ten seconds later.

Every field constraint below is declared here rather than checked in a
handler: a single interceptor enforces them, so a handler that validates its
own input is a bug wherever it appears.

| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| GetStockBySKUs | [GetStockBySKUsRequest](#ecommerce-inventory-v1-GetStockBySKUsRequest) | [GetStockBySKUsResponse](#ecommerce-inventory-v1-GetStockBySKUsResponse) | GetStockBySKUs reads the counts for many SKUs. Every service exposes one of these so a caller can fill a screen without looping single-item calls. |
| GetReservation | [GetReservationRequest](#ecommerce-inventory-v1-GetReservationRequest) | [GetReservationResponse](#ecommerce-inventory-v1-GetReservationResponse) | GetReservation reads one reservation, which is how a saga that lost track of an ambiguous call finds out what actually happened. |
| CreateStockItem | [CreateStockItemRequest](#ecommerce-inventory-v1-CreateStockItemRequest) | [CreateStockItemResponse](#ecommerce-inventory-v1-CreateStockItemResponse) | CreateStockItem starts tracking a SKU, at a starting count.

Explicit rather than folded into AdjustStock as an upsert: a typo in a SKU would otherwise create stock for something nobody sells, and the first sign of it would be an order for goods that do not exist. |
| AdjustStock | [AdjustStockRequest](#ecommerce-inventory-v1-AdjustStockRequest) | [AdjustStockResponse](#ecommerce-inventory-v1-AdjustStockResponse) | AdjustStock moves the available count by a delta — a delivery arriving, a breakage written off.

A delta and not an absolute count, because two receipts landing at once would otherwise overwrite each other and the loss would be silent. It is named for a mutation on purpose: the client retry policy reads method names, and a retried adjustment is a second delivery. |
| ReserveStock | [ReserveStockRequest](#ecommerce-inventory-v1-ReserveStockRequest) | [ReserveStockResponse](#ecommerce-inventory-v1-ReserveStockResponse) | ReserveStock holds stock for an order, for as long as this service&#39;s configured TTL.

Idempotent on order_id rather than on the request as a whole: a saga that retries after an ambiguous timeout is asking whether its hold exists, and gets the existing reservation back unchanged. A *different* set of lines under an order_id that already holds one is a conflict, not a replacement.

Not enough stock is FailedPrecondition with reason OUT_OF_STOCK and the SKU in the metadata, so a caller can name the line that failed. It is deliberately not one of the codes a circuit breaker counts: a run of sold-out SKUs is this service working correctly. |
| CommitReservation | [CommitReservationRequest](#ecommerce-inventory-v1-CommitReservationRequest) | [CommitReservationResponse](#ecommerce-inventory-v1-CommitReservationResponse) | CommitReservation turns a hold into a sale. The held quantity leaves the warehouse rather than returning to available.

It refuses a reservation whose expires_at has passed, even while the reaper has not got to it yet: the capacity is already promised to whoever asks next, and committing on the strength of the reaper being slow is how the same unit gets sold twice. A saga that meets this reserves again or fails the order.

Committing one that is already committed succeeds and changes nothing — every step of a saga is retried eventually. |
| ReleaseReservation | [ReleaseReservationRequest](#ecommerce-inventory-v1-ReleaseReservationRequest) | [ReleaseReservationResponse](#ecommerce-inventory-v1-ReleaseReservationResponse) | ReleaseReservation gives the held stock back. This is the compensating step of a saga that failed after reserving, and it is idempotent for the same reason: compensation runs more than once. |

 



<a name="ecommerce_order_v1_order-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/order/v1/order.proto



<a name="ecommerce-order-v1-Order"></a>

### Order
Order is what the order service tells everyone else about a purchase.

It is the outward shape, not the aggregate: the idempotency record, the saga
bookkeeping, and the optimistic-locking version stay inside the service.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| user_id | [string](#string) |  | Who placed it. Taken from the verified identity on the call that created it, never from a field a client could set. |
| status | [OrderStatus](#ecommerce-order-v1-OrderStatus) |  |  |
| lines | [OrderLine](#ecommerce-order-v1-OrderLine) | repeated |  |
| total | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  | The sum of the lines, in one currency. Stored rather than recomputed on read: it is what the customer agreed to pay, and a total that follows the catalog&#39;s current prices would rewrite that agreement every time one moved. |
| reservation_id | [string](#string) |  | The hold this order has on stock, taken before the order was persisted. Inventory refuses to commit one whose expires_at has passed, so it is also a deadline on how long the rest of the flow has. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |






<a name="ecommerce-order-v1-OrderLine"></a>

### OrderLine
OrderLine is one SKU and what was agreed for it.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| quantity | [int32](#int32) |  |  |
| unit_price | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  | The price at the moment of checkout, copied from the catalog and frozen here. Catalog owns what a thing costs now; an order owns what it cost then, and the two stop agreeing the first time anyone runs a sale. |





 


<a name="ecommerce-order-v1-OrderStatus"></a>

### OrderStatus
OrderStatus is where an order sits in its flow.

The names describe the order rather than the step that moved it, so a state
survives a change in how it is reached: PAID says the money is in, not that a
particular provider called back.

| Name | Number | Description |
| ---- | ------ | ----------- |
| ORDER_STATUS_UNSPECIFIED | 0 |  |
| ORDER_STATUS_PENDING_PAYMENT | 1 | Persisted, stock held, waiting for money. This is what Checkout returns — the entry call does not hold the caller open for the whole workflow. |
| ORDER_STATUS_PAID | 2 | The money is in. The hold on stock is still a hold at this point; turning it into a sale is the next step of the saga. |
| ORDER_STATUS_CANCELLED | 3 | The order will not proceed, and whatever was reserved for it has been given back. A cancelled order is terminal: a customer who wants it after all places a new one, because the stock it held is somebody else&#39;s by now. |


 

 

 



<a name="ecommerce_order_v1_order_service-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/order/v1/order_service.proto



<a name="ecommerce-order-v1-CheckoutLine"></a>

### CheckoutLine
CheckoutLine is one SKU and how many of it. It carries no price: what a thing
costs is the catalog&#39;s answer, and a client that could name its own price
would be naming what it pays.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| sku | [string](#string) |  |  |
| quantity | [int32](#int32) |  |  |






<a name="ecommerce-order-v1-CheckoutRequest"></a>

### CheckoutRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| idempotency_key | [string](#string) |  | The client&#39;s own key for this attempt, echoed on every retry of it.

A field rather than metadata, and this is the one place in the system that is true. pkg/grpcx/client propagates a fixed set of headers because each is a fact about the whole request chain; an idempotency key is a fact about one call, and forwarding it would attach a single key to every hop a fan-out touches. As a field it is validated here and visible in the contract, which is where a client looks to find out that a method honours it at all.

Same key and same body: the first response is replayed. Same key and a different body: refused, because that is a client bug rather than a retry. |
| lines | [CheckoutLine](#ecommerce-order-v1-CheckoutLine) | repeated | What is being bought. Two lines naming one SKU are refused rather than summed: a cart that did that is describing one quantity twice, and which of the two prices was agreed is not something this service can guess. |






<a name="ecommerce-order-v1-CheckoutResponse"></a>

### CheckoutResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| order | [Order](#ecommerce-order-v1-Order) |  |  |






<a name="ecommerce-order-v1-GetOrderRequest"></a>

### GetOrderRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-order-v1-GetOrderResponse"></a>

### GetOrderResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| order | [Order](#ecommerce-order-v1-Order) |  |  |






<a name="ecommerce-order-v1-ListOrdersRequest"></a>

### ListOrdersRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| page_size | [int32](#int32) |  | 0 means 20. |
| page_token | [string](#string) |  | next_page_token from the previous response, opaque to the caller. A cursor and not an offset: orders are inserted while a customer pages, and an offset would skip or repeat whatever moved across the boundary. |






<a name="ecommerce-order-v1-ListOrdersResponse"></a>

### ListOrdersResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| orders | [Order](#ecommerce-order-v1-Order) | repeated |  |
| next_page_token | [string](#string) |  | Empty on the last page. |





 

 

 


<a name="ecommerce-order-v1-OrderService"></a>

### OrderService
OrderService owns orders: what was bought, at what price, and how far the
purchase has got. It is reached over east-west gRPC only.

It is also the orchestrator of the checkout saga. That is not a second
responsibility bolted on: the saga advances an order through its states, and
whoever owns the aggregate owns the rules that protect it. Every step it
drives — reserving stock, capturing payment, committing the hold — belongs to
another service and is asked for rather than reached into.

It owns no stock and no prices. What is on the shelf is inventory&#39;s, what a
thing costs now is catalog&#39;s, and this service copies the price it was quoted
into the order so that the agreement survives the next price change.

| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| Checkout | [CheckoutRequest](#ecommerce-order-v1-CheckoutRequest) | [CheckoutResponse](#ecommerce-order-v1-CheckoutResponse) | Checkout places an order: it reserves the stock, persists the order in a pending state, and returns.

It returns as soon as the order exists rather than when the purchase completes, because the rest of the flow waits on a payment provider and no caller should be held open for that. What the caller gets back is an order to poll or to be told about, not a finished sale.

Reserving happens first and synchronously, so a shopper learns that something is sold out now rather than in an email later. Out of stock comes back as FailedPrecondition with reason OUT_OF_STOCK and the SKU in the metadata, exactly as inventory reported it.

Deliberately not named Get* or Batch*: the client retry policy reads method names to decide what is safe to retry, and a retried checkout that was not deduplicated is a second order. |
| GetOrder | [GetOrderRequest](#ecommerce-order-v1-GetOrderRequest) | [GetOrderResponse](#ecommerce-order-v1-GetOrderResponse) | GetOrder reads one order. A caller may only read their own. |
| ListOrders | [ListOrdersRequest](#ecommerce-order-v1-ListOrdersRequest) | [ListOrdersResponse](#ecommerce-order-v1-ListOrdersResponse) | ListOrders pages through the caller&#39;s own orders, newest first. |

 



<a name="ecommerce_payment_v1_payment-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/payment/v1/payment.proto



<a name="ecommerce-payment-v1-Payment"></a>

### Payment
Payment is what the payment service tells everyone else about one attempt to
collect the money for an order.

It is the outward shape, not the aggregate: the provider&#39;s own request and
response bodies, the idempotency record, and the optimistic-locking version
stay inside the service. One order may have several of these — a declined
card is a finished attempt, and trying again is a new one.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |
| order_id | [string](#string) |  | The order this is collecting for. Not unique: a failed attempt does not stop the customer trying another card. |
| user_id | [string](#string) |  | Who is paying, taken from the verified identity on the call that started it. A payment may only be read by the person who made it. |
| status | [PaymentStatus](#ecommerce-payment-v1-PaymentStatus) |  |  |
| amount | [ecommerce.common.v1.Money](#ecommerce-common-v1-Money) |  | What is being collected, copied from the order at the moment the attempt started. Copied rather than read back, for the reason an order line copies its price: this is the amount that was actually sent to the provider, and it has to stay readable next to what the provider says it charged. |
| provider_reference | [string](#string) |  | The provider&#39;s own identifier for this attempt, empty until the provider has answered. It is what a human reconciles against in the provider&#39;s dashboard, and what an ambiguous timeout is resolved by asking about. |
| failure_reason | [string](#string) |  | Why a failed attempt failed, in the provider&#39;s vocabulary — empty unless status is FAILED. A developer aid and a support aid; it is deliberately not a reason code a client branches on, because the set of them belongs to the provider and changes without this system being told. |
| created_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |
| updated_at | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  |  |





 


<a name="ecommerce-payment-v1-PaymentStatus"></a>

### PaymentStatus
PaymentStatus is where one attempt to collect has got to.

Three states and not four: there is no separate &#34;awaiting the customer&#34;.
Whether the money has not arrived because a 3-D Secure page is still open or
because the provider is slow is the provider&#39;s business, and a state per
reason would be this service tracking a flow it does not own. What the client
needs in order to act is next_action_url, not a finer status.

| Name | Number | Description |
| ---- | ------ | ----------- |
| PAYMENT_STATUS_UNSPECIFIED | 0 |  |
| PAYMENT_STATUS_PENDING | 1 | Sent to the provider, no money yet. The customer may still have something to do, or the provider may simply not have answered. |
| PAYMENT_STATUS_SUCCEEDED | 2 | The money is in. Terminal, and what makes the order paid. |
| PAYMENT_STATUS_FAILED | 3 | The attempt is over and no money moved. Terminal for this attempt, and not for the order: the customer may try again until the hold on their stock expires. |


 

 

 



<a name="ecommerce_payment_v1_payment_service-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## ecommerce/payment/v1/payment_service.proto



<a name="ecommerce-payment-v1-GetPaymentRequest"></a>

### GetPaymentRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| id | [string](#string) |  |  |






<a name="ecommerce-payment-v1-GetPaymentResponse"></a>

### GetPaymentResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| payment | [Payment](#ecommerce-payment-v1-Payment) |  |  |






<a name="ecommerce-payment-v1-GetPaymentsByOrderIDsRequest"></a>

### GetPaymentsByOrderIDsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| order_ids | [string](#string) | repeated | Bounded because the response is: an unbounded list is how a batch read drives its own callee out of memory. |






<a name="ecommerce-payment-v1-GetPaymentsByOrderIDsResponse"></a>

### GetPaymentsByOrderIDsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| payments | [Payment](#ecommerce-payment-v1-Payment) | repeated | Every attempt against the orders asked for, in no guaranteed order and possibly none at all. An order nobody has tried to pay for yet is not an error: one such row must not fail the whole screen a caller is assembling. |






<a name="ecommerce-payment-v1-HandleProviderCallbackRequest"></a>

### HandleProviderCallbackRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| payload | [bytes](#bytes) |  | The provider&#39;s request body, byte for byte as it arrived.

bytes rather than a parsed message, and this is load-bearing: the signature is computed over exactly these bytes, so a hop that decoded the JSON and re-encoded it would produce a body that no longer verifies — a different key order is enough. Whoever forwards this must pass the raw body through. |
| signature | [string](#string) |  | The provider&#39;s signature over that body, taken from its own header. |






<a name="ecommerce-payment-v1-HandleProviderCallbackResponse"></a>

### HandleProviderCallbackResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| payment | [Payment](#ecommerce-payment-v1-Payment) |  | The attempt as it stands after the callback was applied, so a caller that wants to log what changed does not need a second read.

A callback naming an attempt this service does not know about is not an error and leaves this empty: providers send events for things they were never asked to do, and answering 404 would make them retry forever. |






<a name="ecommerce-payment-v1-InitiatePaymentRequest"></a>

### InitiatePaymentRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| order_id | [string](#string) |  | The order to collect for. What it costs is read from the order service rather than taken from this request. |
| idempotency_key | [string](#string) |  | The client&#39;s own key for this attempt, echoed on every retry of it.

A field rather than metadata, for the reason CheckoutRequest states: an idempotency key is a fact about one call rather than about the request chain, and forwarding it as a header would attach a single key to every hop a fan-out touches.

It deduplicates the *submit*, not the order: a customer whose card was declined and who tries again is sending a new key, and gets a new attempt. |
| method | [string](#string) |  | Which method the customer chose, in this system&#39;s vocabulary rather than the provider&#39;s. Empty means the provider&#39;s default. |






<a name="ecommerce-payment-v1-InitiatePaymentResponse"></a>

### InitiatePaymentResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| payment | [Payment](#ecommerce-payment-v1-Payment) |  |  |
| next_action_url | [string](#string) |  | Where to send the customer to finish paying — a 3-D Secure page, a hosted form, a QR code. Empty when there is nothing for them to do and the answer is simply on its way.

Not stored on the Payment: it is a fact about this response, it expires, and a URL that authorises a charge is not something to hand back on every later read of the attempt. |





 

 

 


<a name="ecommerce-payment-v1-PaymentService"></a>

### PaymentService
PaymentService owns attempts to collect money: what was charged, to whom, by
which provider, and how it ended. It is reached over east-west gRPC only.

It owns no orders. How much an order costs and whether it is still waiting to
be paid are the order service&#39;s answers, read synchronously before an attempt
starts — a client that could name its own amount would be naming what it
pays. What this service owns is everything after that: the call to the
provider, the record of what it said, and the fact published when the money
is in or gone for good.

It is deliberately not the orchestrator. Order drives the checkout saga and
consumes PaymentSucceeded and PaymentFailed to advance or compensate; this
service publishes what happened and decides nothing about the order.

Every field constraint below is declared here rather than checked in a
handler: a single interceptor enforces them, so a handler that validates its
own input is a bug wherever it appears.

| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| InitiatePayment | [InitiatePaymentRequest](#ecommerce-payment-v1-InitiatePaymentRequest) | [InitiatePaymentResponse](#ecommerce-payment-v1-InitiatePaymentResponse) | InitiatePayment starts one attempt to collect for an order and returns as soon as the provider has been told about it.

It does not wait for the money. A card may need a 3-D Secure page and a QR code needs somebody to scan it, so what comes back is a PENDING payment and wherever the customer has to go next; the outcome arrives later as a callback from the provider.

An order that is not still waiting for payment is FailedPrecondition with reason ORDER_NOT_PAYABLE — already paid, cancelled, or somebody else&#39;s.

Deliberately not named Get* or Batch*: the client retry policy reads method names to decide what is safe to retry, and a retried attempt that was not deduplicated is a second charge. |
| GetPayment | [GetPaymentRequest](#ecommerce-payment-v1-GetPaymentRequest) | [GetPaymentResponse](#ecommerce-payment-v1-GetPaymentResponse) | GetPayment reads one attempt. A caller may only read their own. |
| GetPaymentsByOrderIDs | [GetPaymentsByOrderIDsRequest](#ecommerce-payment-v1-GetPaymentsByOrderIDsRequest) | [GetPaymentsByOrderIDsResponse](#ecommerce-payment-v1-GetPaymentsByOrderIDsResponse) | GetPaymentsByOrderIDs reads the attempts made against many orders, so a caller can fill an order-history screen without looping single-item calls. |
| HandleProviderCallback | [HandleProviderCallbackRequest](#ecommerce-payment-v1-HandleProviderCallbackRequest) | [HandleProviderCallbackResponse](#ecommerce-payment-v1-HandleProviderCallbackResponse) | HandleProviderCallback takes a webhook exactly as the provider sent it and settles the attempt it names.

The BFF forwards the request here without interpreting it: verifying the signature needs the provider&#39;s secret and knowing what the body means is this service&#39;s business, and a BFF has neither. It arrives as bytes and a signature for that reason.

Idempotent, because a provider redelivers: a callback for an attempt that is already settled is accepted and changes nothing. Not named for a read even so — it writes, and the retry policy must not send it again on its own. |

 



## Scalar Value Types

| .proto Type | Notes | C++ | Java | Python | Go | C# | PHP | Ruby |
| ----------- | ----- | --- | ---- | ------ | -- | -- | --- | ---- |
| <a name="double" /> double |  | double | double | float | float64 | double | float | Float |
| <a name="float" /> float |  | float | float | float | float32 | float | float | Float |
| <a name="int32" /> int32 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint32 instead. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="int64" /> int64 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint64 instead. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="uint32" /> uint32 | Uses variable-length encoding. | uint32 | int | int/long | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="uint64" /> uint64 | Uses variable-length encoding. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum or Fixnum (as required) |
| <a name="sint32" /> sint32 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int32s. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sint64" /> sint64 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int64s. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="fixed32" /> fixed32 | Always four bytes. More efficient than uint32 if values are often greater than 2^28. | uint32 | int | int | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="fixed64" /> fixed64 | Always eight bytes. More efficient than uint64 if values are often greater than 2^56. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum |
| <a name="sfixed32" /> sfixed32 | Always four bytes. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sfixed64" /> sfixed64 | Always eight bytes. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="bool" /> bool |  | bool | boolean | boolean | bool | bool | boolean | TrueClass/FalseClass |
| <a name="string" /> string | A string must always contain UTF-8 encoded or 7-bit ASCII text. | string | String | str/unicode | string | string | string | String (UTF-8) |
| <a name="bytes" /> bytes | May contain any arbitrary sequence of bytes. | string | ByteString | str | []byte | ByteString | string | String (ASCII-8BIT) |

