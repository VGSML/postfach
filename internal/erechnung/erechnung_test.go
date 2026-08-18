package erechnung

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

const ublSample = `<?xml version="1.0" encoding="UTF-8"?>
<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
         xmlns:cac="urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2"
         xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">
  <cbc:CustomizationID>urn:cen.eu:en16931:2017#compliant#urn:xeinkauf.de:kosit:xrechnung_3.0</cbc:CustomizationID>
  <cbc:ID>RE-2026-4711</cbc:ID>
  <cbc:IssueDate>2026-08-06</cbc:IssueDate>
  <cbc:DueDate>2026-08-20</cbc:DueDate>
  <cbc:InvoiceTypeCode>380</cbc:InvoiceTypeCode>
  <cbc:DocumentCurrencyCode>EUR</cbc:DocumentCurrencyCode>
  <cbc:BuyerReference>04011000-1234512345-06</cbc:BuyerReference>
  <cac:AccountingSupplierParty><cac:Party>
    <cac:PartyTaxScheme><cbc:CompanyID>DE123456789</cbc:CompanyID></cac:PartyTaxScheme>
    <cac:PartyLegalEntity><cbc:RegistrationName>Berlin Recycling GmbH</cbc:RegistrationName></cac:PartyLegalEntity>
  </cac:Party></cac:AccountingSupplierParty>
  <cac:AccountingCustomerParty><cac:Party>
    <cac:PartyLegalEntity><cbc:RegistrationName>ICT Invest GmbH</cbc:RegistrationName></cac:PartyLegalEntity>
  </cac:Party></cac:AccountingCustomerParty>
  <cac:PaymentMeans>
    <cbc:PaymentID>RE-2026-4711</cbc:PaymentID>
    <cac:PayeeFinancialAccount><cbc:ID>DE63100100100283508108</cbc:ID></cac:PayeeFinancialAccount>
  </cac:PaymentMeans>
  <cac:TaxTotal><cbc:TaxAmount currencyID="EUR">190.00</cbc:TaxAmount></cac:TaxTotal>
  <cac:LegalMonetaryTotal>
    <cbc:LineExtensionAmount currencyID="EUR">1000.00</cbc:LineExtensionAmount>
    <cbc:TaxExclusiveAmount currencyID="EUR">1000.00</cbc:TaxExclusiveAmount>
    <cbc:TaxInclusiveAmount currencyID="EUR">1190.00</cbc:TaxInclusiveAmount>
    <cbc:PayableAmount currencyID="EUR">1190.00</cbc:PayableAmount>
  </cac:LegalMonetaryTotal>
  <cac:InvoiceLine>
    <cbc:ID>1</cbc:ID>
    <cbc:InvoicedQuantity unitCode="C62">2</cbc:InvoicedQuantity>
    <cbc:LineExtensionAmount currencyID="EUR">1000.00</cbc:LineExtensionAmount>
    <cac:Item><cbc:Name>Entsorgung Restabfall 1.100 l</cbc:Name></cac:Item>
  </cac:InvoiceLine>
</Invoice>`

const ciiSample = `<?xml version="1.0" encoding="UTF-8"?>
<rsm:CrossIndustryInvoice xmlns:rsm="urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100"
    xmlns:ram="urn:un:unece:uncefact:data:standard:ReusableAggregateBusinessInformationEntity:100"
    xmlns:udt="urn:un:unece:uncefact:data:standard:UnqualifiedDataType:100">
  <rsm:ExchangedDocumentContext>
    <ram:GuidelineSpecifiedDocumentContextParameter><ram:ID>urn:cen.eu:en16931:2017#conformant#urn:factur-x.eu:1p0:extended</ram:ID></ram:GuidelineSpecifiedDocumentContextParameter>
  </rsm:ExchangedDocumentContext>
  <rsm:ExchangedDocument>
    <ram:ID>40023900</ram:ID>
    <ram:TypeCode>380</ram:TypeCode>
    <ram:IssueDateTime><udt:DateTimeString format="102">20260806</udt:DateTimeString></ram:IssueDateTime>
  </rsm:ExchangedDocument>
  <rsm:SupplyChainTradeTransaction>
    <ram:IncludedSupplyChainTradeLineItem>
      <ram:AssociatedDocumentLineDocument><ram:LineID>1</ram:LineID></ram:AssociatedDocumentLineDocument>
      <ram:SpecifiedTradeProduct><ram:Name>Reparatur Combidämpfer SCC61E</ram:Name></ram:SpecifiedTradeProduct>
      <ram:SpecifiedLineTradeDelivery><ram:BilledQuantity unitCode="HUR">3.5</ram:BilledQuantity></ram:SpecifiedLineTradeDelivery>
      <ram:SpecifiedLineTradeSettlement>
        <ram:SpecifiedTradeSettlementLineMonetarySummation><ram:LineTotalAmount>420.00</ram:LineTotalAmount></ram:SpecifiedTradeSettlementLineMonetarySummation>
      </ram:SpecifiedLineTradeSettlement>
    </ram:IncludedSupplyChainTradeLineItem>
    <ram:ApplicableHeaderTradeAgreement>
      <ram:BuyerReference>991-01234-56</ram:BuyerReference>
      <ram:SellerTradeParty>
        <ram:Name>Küchentechnik Karnick</ram:Name>
        <ram:SpecifiedTaxRegistration><ram:ID schemeID="VA">DE987654321</ram:ID></ram:SpecifiedTaxRegistration>
      </ram:SellerTradeParty>
      <ram:BuyerTradeParty><ram:Name>Schlossparkhotel Schkopau</ram:Name></ram:BuyerTradeParty>
    </ram:ApplicableHeaderTradeAgreement>
    <ram:ApplicableHeaderTradeSettlement>
      <ram:PaymentReference>40023900</ram:PaymentReference>
      <ram:InvoiceCurrencyCode>EUR</ram:InvoiceCurrencyCode>
      <ram:SpecifiedTradeSettlementPaymentMeans>
        <ram:PayeePartyCreditorFinancialAccount><ram:IBANID>DE02120300000000202051</ram:IBANID></ram:PayeePartyCreditorFinancialAccount>
      </ram:SpecifiedTradeSettlementPaymentMeans>
      <ram:SpecifiedTradePaymentTerms>
        <ram:DueDateDateTime><udt:DateTimeString format="102">20260820</udt:DateTimeString></ram:DueDateDateTime>
      </ram:SpecifiedTradePaymentTerms>
      <ram:SpecifiedTradeSettlementHeaderMonetarySummation>
        <ram:LineTotalAmount>420.00</ram:LineTotalAmount>
        <ram:TaxBasisTotalAmount>420.00</ram:TaxBasisTotalAmount>
        <ram:TaxTotalAmount currencyID="EUR">79.80</ram:TaxTotalAmount>
        <ram:GrandTotalAmount>499.80</ram:GrandTotalAmount>
        <ram:DuePayableAmount>499.80</ram:DuePayableAmount>
      </ram:SpecifiedTradeSettlementHeaderMonetarySummation>
    </ram:ApplicableHeaderTradeSettlement>
  </rsm:SupplyChainTradeTransaction>
</rsm:CrossIndustryInvoice>`

func TestParseUBL(t *testing.T) {
	inv, err := ParseXML([]byte(ublSample))
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		"format":     inv.Format,
		"number":     inv.InvoiceNumber,
		"issue":      inv.IssueDate,
		"due":        inv.DueDate,
		"currency":   inv.Currency,
		"seller":     inv.Seller.Name,
		"vat":        inv.Seller.VATID,
		"buyer":      inv.Buyer.Name,
		"buyer_ref":  inv.BuyerReference,
		"iban":       inv.IBAN,
		"net":        inv.TotalNet,
		"tax":        inv.TotalTax,
		"gross":      inv.TotalGross,
		"payable":    inv.PayableAmount,
		"line0_name": inv.Lines[0].Description,
	}
	want := map[string]string{
		"format": "ubl", "number": "RE-2026-4711", "issue": "2026-08-06",
		"due": "2026-08-20", "currency": "EUR", "seller": "Berlin Recycling GmbH",
		"vat": "DE123456789", "buyer": "ICT Invest GmbH",
		"buyer_ref": "04011000-1234512345-06", "iban": "DE63100100100283508108",
		"net": "1000.00", "tax": "190.00", "gross": "1190.00", "payable": "1190.00",
		"line0_name": "Entsorgung Restabfall 1.100 l",
	}
	for k, got := range checks {
		if got != want[k] {
			t.Errorf("%s = %q, want %q", k, got, want[k])
		}
	}
}

func TestParseCII(t *testing.T) {
	inv, err := ParseXML([]byte(ciiSample))
	if err != nil {
		t.Fatal(err)
	}
	checks := [][2]string{
		{inv.Format, "cii"},
		{inv.InvoiceNumber, "40023900"},
		{inv.TypeCode, "380"},
		{inv.IssueDate, "2026-08-06"},
		{inv.DueDate, "2026-08-20"},
		{inv.Currency, "EUR"},
		{inv.Seller.Name, "Küchentechnik Karnick"},
		{inv.Seller.VATID, "DE987654321"},
		{inv.Buyer.Name, "Schlossparkhotel Schkopau"},
		{inv.IBAN, "DE02120300000000202051"},
		{inv.TotalNet, "420.00"},
		{inv.TotalTax, "79.80"},
		{inv.TotalGross, "499.80"},
		{inv.PayableAmount, "499.80"},
		{inv.Lines[0].Description, "Reparatur Combidämpfer SCC61E"},
		{inv.Lines[0].Quantity, "3.5"},
		{inv.Lines[0].Unit, "HUR"},
	}
	for i, c := range checks {
		if c[0] != c[1] {
			t.Errorf("check %d: got %q, want %q", i, c[0], c[1])
		}
	}
}

func TestParseXMLRejectsOther(t *testing.T) {
	if _, err := ParseXML([]byte(`<?xml version="1.0"?><foo/>`)); err == nil {
		t.Error("non-invoice XML accepted")
	}
}

// TestExtractFromPDF builds a real ZUGFeRD-style PDF (page + embedded
// factur-x.xml) with pdfcpu and extracts the XML back.
func TestExtractFromPDF(t *testing.T) {
	dir := t.TempDir()

	imgPath := filepath.Join(dir, "page.png")
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	f.Close()

	pdfPath := filepath.Join(dir, "invoice.pdf")
	if err := api.ImportImagesFile([]string{imgPath}, pdfPath, nil, nil); err != nil {
		t.Fatal(err)
	}
	xmlPath := filepath.Join(dir, "factur-x.xml")
	if err := os.WriteFile(xmlPath, []byte(ciiSample), 0o600); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "facturx.pdf")
	if err := api.AddAttachmentsFile(pdfPath, outPath, []string{xmlPath}, false, nil); err != nil {
		t.Fatal(err)
	}

	pdf, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	data, name, err := ExtractFromPDF(pdf)
	if err != nil {
		t.Fatal(err)
	}
	if name != "factur-x.xml" {
		t.Errorf("embedded name = %q", name)
	}
	inv, err := ParseXML(data)
	if err != nil {
		t.Fatal(err)
	}
	if inv.InvoiceNumber != "40023900" {
		t.Errorf("roundtrip invoice number = %q", inv.InvoiceNumber)
	}
}
