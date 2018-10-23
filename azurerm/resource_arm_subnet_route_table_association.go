package azurerm

import (
	"fmt"
	"log"

	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2018-08-01/network"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/azure"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/utils"
)

func resourceArmSubnetRouteTableAssociation() *schema.Resource {
	return &schema.Resource{
		Create: resourceArmSubnetRouteTableAssociationCreate,
		Read:   resourceArmSubnetRouteTableAssociationRead,
		Delete: resourceArmSubnetRouteTableAssociationDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"subnet_id": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: azure.ValidateResourceID,
			},

			"route_table_id": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: azure.ValidateResourceID,
			},
		},
	}
}

func resourceArmSubnetRouteTableAssociationCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ArmClient).subnetClient
	ctx := meta.(*ArmClient).StopContext

	log.Printf("[INFO] preparing arguments for Subnet <-> Route Table Association creation.")

	subnetId := d.Get("subnet_id").(string)
	routeTableId := d.Get("route_table_id").(string)

	parsedSubnetId, err := parseAzureResourceID(subnetId)
	if err != nil {
		return err
	}

	routeTableName, err := parseRouteTableName(routeTableId)
	if err != nil {
		return err
	}

	azureRMLockByName(routeTableName, routeTableResourceName)
	defer azureRMUnlockByName(routeTableName, routeTableResourceName)

	subnetName := parsedSubnetId.Path["subnets"]
	virtualNetworkName := parsedSubnetId.Path["virtualNetworks"]
	resourceGroup := parsedSubnetId.ResourceGroup

	azureRMLockByName(virtualNetworkName, virtualNetworkResourceName)
	defer azureRMUnlockByName(virtualNetworkName, virtualNetworkResourceName)

	subnet, err := client.Get(ctx, resourceGroup, virtualNetworkName, subnetName, "")
	if err != nil {
		if utils.ResponseWasNotFound(subnet.Response) {
			return fmt.Errorf("Subnet %q (Virtual Network %q / Resource Group %q) was not found!", subnetName, virtualNetworkName, resourceGroup)
		}

		return fmt.Errorf("Error retrieving Subnet %q (Virtual Network %q / Resource Group %q): %+v", subnetName, virtualNetworkName, resourceGroup, err)
	}

	if props := subnet.SubnetPropertiesFormat; props != nil {
		props.RouteTable = &network.RouteTable{
			ID: utils.String(routeTableId),
		}
	}

	future, err := client.CreateOrUpdate(ctx, resourceGroup, virtualNetworkName, subnetName, subnet)
	if err != nil {
		return fmt.Errorf("Error updating Route Table Association for Subnet %q (Virtual Network %q / Resource Group %q): %+v", subnetName, virtualNetworkName, resourceGroup, err)
	}

	err = future.WaitForCompletionRef(ctx, client.Client)
	if err != nil {
		return fmt.Errorf("Error waiting for completion of Route Table Association for Subnet %q (VN %q / Resource Group %q): %+v", subnetName, virtualNetworkName, resourceGroup, err)
	}

	read, err := client.Get(ctx, resourceGroup, virtualNetworkName, subnetName, "")
	if err != nil {
		return fmt.Errorf("Error retrieving Subnet %q (Virtual Network %q / Resource Group %q): %+v", subnetName, virtualNetworkName, resourceGroup, err)
	}

	d.SetId(*read.ID)

	return resourceArmSubnetRouteTableAssociationRead(d, meta)
}

func resourceArmSubnetRouteTableAssociationRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ArmClient).subnetClient
	ctx := meta.(*ArmClient).StopContext

	id, err := parseAzureResourceID(d.Id())
	if err != nil {
		return err
	}
	resourceGroup := id.ResourceGroup
	virtualNetworkName := id.Path["virtualNetworks"]
	subnetName := id.Path["subnets"]

	resp, err := client.Get(ctx, resourceGroup, virtualNetworkName, subnetName, "")

	if err != nil {
		if utils.ResponseWasNotFound(resp.Response) {
			log.Printf("[DEBUG] Subnet %q (Virtual Network %q / Resource Group %q) could not be found - removing from state!", subnetName, virtualNetworkName, resourceGroup)
			d.SetId("")
			return nil
		}
		return fmt.Errorf("Error retrieving Subnet %q (Virtual Network %q / Resource Group %q): %+v", subnetName, virtualNetworkName, resourceGroup, err)
	}

	props := resp.SubnetPropertiesFormat
	if props == nil {
		return fmt.Errorf("Error: `properties` was nil for Subnet %q (Virtual Network %q / Resource Group %q)", subnetName, virtualNetworkName, resourceGroup)
	}

	routeTable := props.RouteTable
	if routeTable == nil {
		log.Printf("[DEBUG] Subnet %q (Virtual Network %q / Resource Group %q) doesn't have a Route Table - removing from state!", subnetName, virtualNetworkName, resourceGroup)
		d.SetId("")
		return nil
	}

	d.Set("subnet_id", resp.ID)
	d.Set("route_table_id", routeTable.ID)

	return nil
}

func resourceArmSubnetRouteTableAssociationDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ArmClient).subnetClient
	ctx := meta.(*ArmClient).StopContext

	id, err := parseAzureResourceID(d.Id())
	if err != nil {
		return err
	}
	resourceGroup := id.ResourceGroup
	virtualNetworkName := id.Path["virtualNetworks"]
	subnetName := id.Path["subnets"]

	// retrieve the subnet
	read, err := client.Get(ctx, resourceGroup, virtualNetworkName, subnetName, "")
	if err != nil {
		if utils.ResponseWasNotFound(read.Response) {
			log.Printf("[DEBUG] Subnet %q (Virtual Network %q / Resource Group %q) could not be found - removing from state!", subnetName, virtualNetworkName, resourceGroup)
			return nil
		}

		return fmt.Errorf("Error retrieving Subnet %q (Virtual Network %q / Resource Group %q): %+v", subnetName, virtualNetworkName, resourceGroup, err)
	}

	props := read.SubnetPropertiesFormat
	if props == nil {
		return fmt.Errorf("`Properties` was nil for Subnet %q (Virtual Network %q / Resource Group %q)", subnetName, virtualNetworkName, resourceGroup)
	}

	if props.RouteTable == nil || props.RouteTable.ID == nil {
		log.Printf("[DEBUG] Subnet %q (Virtual Network %q / Resource Group %q) has no Route Table - removing from state!", subnetName, virtualNetworkName, resourceGroup)
		return nil
	}

	// once we have the route table id to lock on, lock on that
	routeTableName, err := parseRouteTableName(*props.RouteTable.ID)
	if err != nil {
		return err
	}

	azureRMLockByName(routeTableName, routeTableResourceName)
	defer azureRMUnlockByName(routeTableName, routeTableResourceName)

	azureRMLockByName(virtualNetworkName, virtualNetworkResourceName)
	defer azureRMUnlockByName(virtualNetworkName, virtualNetworkResourceName)

	azureRMLockByName(subnetName, subnetResourceName)
	defer azureRMUnlockByName(subnetName, subnetResourceName)

	// then re-retrieve it to ensure we've got the latest state
	read, err = client.Get(ctx, resourceGroup, virtualNetworkName, subnetName, "")
	if err != nil {
		if utils.ResponseWasNotFound(read.Response) {
			log.Printf("[DEBUG] Subnet %q (Virtual Network %q / Resource Group %q) could not be found - removing from state!", subnetName, virtualNetworkName, resourceGroup)
			return nil
		}

		return fmt.Errorf("Error retrieving Subnet %q (Virtual Network %q / Resource Group %q): %+v", subnetName, virtualNetworkName, resourceGroup, err)
	}

	read.SubnetPropertiesFormat.RouteTable = nil

	future, err := client.CreateOrUpdate(ctx, resourceGroup, virtualNetworkName, subnetName, read)
	if err != nil {
		return fmt.Errorf("Error removing Route Table Association from Subnet %q (Virtual Network %q / Resource Group %q): %+v", subnetName, virtualNetworkName, resourceGroup, err)
	}

	if err = future.WaitForCompletionRef(ctx, client.Client); err != nil {
		return fmt.Errorf("Error waiting for removal of Route Table Association from Subnet %q (Virtual Network %q / Resource Group %q): %+v", subnetName, virtualNetworkName, resourceGroup, err)
	}

	return nil
}
